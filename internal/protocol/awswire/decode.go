package awswire

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
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

func Decode(req *http.Request, response *http.Response, requestBody, responseBody []byte) (Metadata, error) {
	if req == nil || req.URL == nil {
		return Metadata{}, fmt.Errorf("AWS metadata requires a request URL")
	}
	m := Metadata{}
	query, err := url.ParseQuery(req.URL.RawQuery)
	if err != nil {
		return m, fmt.Errorf("AWS request query: %w", err)
	}
	credential := query.Get("X-Amz-Credential")
	if authorization := req.Header.Get("Authorization"); strings.HasPrefix(authorization, "AWS4-") {
		_, rest, found := strings.Cut(authorization, "Credential=")
		if !found {
			return m, fmt.Errorf("AWS authorization has no credential scope")
		}
		credential, _, _ = strings.Cut(rest, ",")
	}
	if credential != "" {
		parts := strings.Split(strings.TrimSpace(credential), "/")
		if len(parts) != 5 || parts[0] == "" || len(parts[1]) != 8 || parts[2] == "" || parts[3] == "" || parts[4] != "aws4_request" {
			return m, fmt.Errorf("invalid AWS credential scope")
		}
		m.Region, m.Service = parts[2], parts[3]
	}
	m.Operation = query.Get("Action")
	if target := req.Header.Get("X-Amz-Target"); target != "" {
		_, operation, ok := strings.Cut(target, ".")
		if !ok || operation == "" {
			return m, fmt.Errorf("invalid AWS operation target")
		}
		m.Operation = operation
	}
	mediaType, _, _ := mime.ParseMediaType(req.Header.Get("Content-Type"))
	if mediaType == "application/x-www-form-urlencoded" && len(requestBody) > 0 {
		form, err := url.ParseQuery(string(requestBody))
		if err != nil {
			return m, fmt.Errorf("AWS query payload: %w", err)
		}
		m.Operation = form.Get("Action")
	}
	if response == nil {
		return m, nil
	}
	m.RequestID = response.Header.Get("X-Amzn-Requestid")
	if m.RequestID == "" {
		m.RequestID = response.Header.Get("X-Amz-Request-Id")
	}
	if response.StatusCode >= 400 && len(responseBody) > 0 {
		if err := m.decodeError(responseBody); err != nil {
			return m, err
		}
	}
	return m, nil
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
		if m.ErrorCode == "" {
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
