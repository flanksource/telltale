package profilepublication

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"github.com/flanksource/commons-db/recordstore"
	"github.com/flanksource/commons-db/recordstore/recordresults"
	"github.com/flanksource/commons-db/recordstore/sampleframe"
	"github.com/google/uuid"
)

const IdentityKind = "otel_profile_identity"
const IdentityStream = "profile-identities"

type Identity struct {
	Key       string `json:"key" filter:"exact"`
	Digest    string `json:"digest"`
	ProfileID string `json:"profileId"`
	Kind      string `json:"kind"`
}

func ResultType() recordresults.ResultType[Identity] {
	return recordresults.ResultType[Identity]{Kind: IdentityKind, Title: "Received profile identities", KeyColumn: "key", Retention: recordstore.RetainRows}
}

func IsProfile(frame sampleframe.Frame) bool {
	_, id := frame.Summary["profileId"]
	_, version := frame.Summary["profilesDevelopmentVersion"]
	return id || version
}

func identityKeys(kind, source, captureID, profileID string, sequence int64) []string {
	var tuple []byte
	for _, value := range []string{kind, source, captureID} {
		tuple = strconv.AppendInt(tuple, int64(len(value)), 10)
		tuple = append(tuple, ':')
		tuple = append(tuple, value...)
	}
	tuple = strconv.AppendInt(tuple, sequence, 10)
	digest := sha256.Sum256(tuple)
	return []string{"profile/" + profileID, "sequence/" + hex.EncodeToString(digest[:])}
}

func frameKeys(kind string, frame sampleframe.Frame) ([]string, string, string, error) {
	id, _ := frame.Summary["profileId"].(string)
	capture, _ := frame.Summary["captureId"].(string)
	digest, _ := frame.Summary["digest"].(string)
	parsed, err := uuid.Parse(id)
	if err != nil || parsed == uuid.Nil || capture == "" || frame.Tick <= 0 {
		return nil, "", "", fmt.Errorf("profile publication requires a nonzero UUID, capture and positive producer sequence")
	}
	decoded, err := hex.DecodeString(digest)
	if err != nil || len(decoded) != sha256.Size {
		return nil, "", "", fmt.Errorf("profile publication requires a SHA-256 semantic digest")
	}
	source, found := strings.CutSuffix(frame.Source, "/"+capture)
	if !found || source == "" {
		return nil, "", "", fmt.Errorf("profile frame source must include its producer and capture identity")
	}
	return identityKeys(kind, source, capture, id, frame.Tick), digest, id, nil
}
