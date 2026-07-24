package contentassistant

import (
	"strings"
	"testing"
)

func TestTextDiffPreservesOriginalAndProposal(t *testing.T) {
	original := "Ciao mondo 👋 — oggi piove."
	proposed := "Ciao community 👋 — oggi splende il sole."
	diff := textDiff(original, proposed)

	var reconstructedOriginal strings.Builder
	var reconstructedProposal strings.Builder
	for _, segment := range diff {
		switch segment.Operation {
		case DiffEqual:
			reconstructedOriginal.WriteString(segment.Text)
			reconstructedProposal.WriteString(segment.Text)
		case DiffDelete:
			reconstructedOriginal.WriteString(segment.Text)
		case DiffInsert:
			reconstructedProposal.WriteString(segment.Text)
		default:
			t.Fatalf("unknown diff operation %q", segment.Operation)
		}
	}
	if reconstructedOriginal.String() != original {
		t.Fatalf("reconstructed original = %q", reconstructedOriginal.String())
	}
	if reconstructedProposal.String() != proposed {
		t.Fatalf("reconstructed proposal = %q", reconstructedProposal.String())
	}
}

func TestTextDiffHandlesInsertOnlyAndEqualText(t *testing.T) {
	insert := textDiff("", "Nuovo")
	if len(insert) != 1 ||
		insert[0].Operation != DiffInsert ||
		insert[0].Text != "Nuovo" {
		t.Fatalf("insert diff = %#v", insert)
	}

	equal := textDiff("uguale", "uguale")
	if len(equal) != 1 ||
		equal[0].Operation != DiffEqual ||
		equal[0].Text != "uguale" {
		t.Fatalf("equal diff = %#v", equal)
	}
}
