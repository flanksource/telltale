package awswire

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
)

type Metadata struct {
	Service   string `json:"service,omitempty"`
	Region    string `json:"region,omitempty"`
	Operation string `json:"operation,omitempty"`
	RequestID string `json:"requestId,omitempty"`
	ErrorCode string `json:"errorCode,omitempty"`
}

type Scope struct {
	Service string
	Region  string
}

// SigningScope projects the non-credential part of a SigV4 signing scope.
func SigningScope(req *http.Request) (Scope, error) {
	if req == nil || req.URL == nil {
		return Scope{}, fmt.Errorf("AWS signing scope requires a request URL")
	}
	credential := req.URL.Query().Get("X-Amz-Credential")
	if authorization := req.Header.Get("Authorization"); strings.HasPrefix(authorization, "AWS4-") {
		_, rest, found := strings.Cut(authorization, "Credential=")
		if !found {
			return Scope{}, fmt.Errorf("AWS authorization has no credential scope")
		}
		credential, _, _ = strings.Cut(rest, ",")
	}
	if credential == "" {
		return Scope{}, nil
	}
	parts := strings.Split(strings.TrimSpace(credential), "/")
	if len(parts) == 4 && parts[0] != "" && len(parts[1]) == 8 && scopeLabel(parts[2]) && parts[3] == "aws4_request" {
		return Scope{Service: parts[2]}, nil // SigV4a omits the region from the scope.
	}
	if len(parts) != 5 || parts[0] == "" || len(parts[1]) != 8 || !scopeLabel(parts[2]) || !scopeLabel(parts[3]) || parts[4] != "aws4_request" {
		return Scope{}, fmt.Errorf("invalid AWS credential scope")
	}
	return Scope{Region: parts[2], Service: parts[3]}, nil
}

func scopeLabel(value string) bool {
	return len(value) > 0 && len(value) <= 64 && strings.IndexFunc(value, func(c rune) bool {
		return (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-'
	}) == -1
}

func Decode(req *http.Request, response *http.Response, requestBody, responseBody []byte) (Metadata, error) {
	if req == nil || req.URL == nil {
		return Metadata{}, fmt.Errorf("AWS metadata requires a request URL")
	}
	scope, scopeErr := SigningScope(req)
	operation, operationErr := decodeOperation(req, requestBody)
	m := Metadata{Service: scope.Service, Region: scope.Region, Operation: operation}
	if response == nil {
		return m, errors.Join(scopeErr, operationErr)
	}
	m.RequestID = response.Header.Get("X-Amzn-Requestid")
	if m.RequestID == "" {
		m.RequestID = response.Header.Get("X-Amz-Request-Id")
	}
	if m.RequestID == "" {
		m.RequestID = response.Header.Get("X-Amzn-Request-Id")
	}
	var responseErr error
	if response.StatusCode >= 400 && len(responseBody) > 0 {
		responseErr = m.decodeError(responseBody)
	}
	return m, errors.Join(scopeErr, operationErr, responseErr)
}

func decodeOperation(req *http.Request, requestBody []byte) (string, error) {
	query, err := url.ParseQuery(req.URL.RawQuery)
	if err != nil {
		return query.Get("Action"), fmt.Errorf("AWS request query: %w", err)
	}
	operation := query.Get("Action")
	if target := req.Header.Get("X-Amz-Target"); target != "" {
		_, name, ok := strings.Cut(target, ".")
		if !ok || name == "" {
			return operation, fmt.Errorf("invalid AWS operation target")
		}
		operation = name
	}
	mediaType, _, _ := mime.ParseMediaType(req.Header.Get("Content-Type"))
	if mediaType == "application/x-www-form-urlencoded" && len(requestBody) > 0 {
		form, err := url.ParseQuery(string(requestBody))
		if err != nil {
			return operation, fmt.Errorf("AWS query payload: %w", err)
		}
		if action := form.Get("Action"); action != "" {
			operation = action
		}
	}
	return operation, nil
}

func (m *Metadata) decodeError(body []byte) error {
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		return nil
	}
	if body[0] == '{' {
		var fields struct {
			Type string `json:"__type"`
			Code string `json:"code"`
		}
		if err := json.Unmarshal(body, &fields); err != nil {
			return fmt.Errorf("AWS JSON error: %w", err)
		}
		m.ErrorCode = fields.Code
		if m.ErrorCode == "" || strings.Contains(m.ErrorCode, "****") || m.ErrorCode == "[REDACTED]" {
			_, m.ErrorCode, _ = strings.Cut(fields.Type, "#")
			if m.ErrorCode == "" {
				m.ErrorCode = fields.Type
			}
		}
		return nil
	}
	if body[0] != '<' {
		return nil
	}
	d := xml.NewDecoder(bytes.NewReader(body))
	for {
		token, err := d.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("AWS XML error: %w", err)
		}
		start, ok := token.(xml.StartElement)
		if ok && start.Name.Local == "Code" {
			if err := d.DecodeElement(&m.ErrorCode, &start); err != nil {
				return fmt.Errorf("AWS XML error code: %w", err)
			}
		}
	}
}
