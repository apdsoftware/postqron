package collaboration

import (
	"encoding/json"
	"os"
	"testing"
)

func TestEventContractIsValidJSONAndRejectsContentPayloadByConstruction(t *testing.T) {
	source, err := os.ReadFile("contracts/events/v1/collaboration-event.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(source) {
		t.Fatal("collaboration event schema is not valid JSON")
	}
	var contract struct {
		Properties struct {
			Data struct {
				Properties map[string]any `json:"properties"`
			} `json:"data"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(source, &contract); err != nil {
		t.Fatal(err)
	}
	if _, exposesBody := contract.Properties.Data.Properties["body"]; exposesBody {
		t.Fatal("F9 event contract must not expose comment bodies")
	}
}
