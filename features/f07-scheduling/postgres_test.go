package scheduling

import (
	"reflect"
	"testing"
)

func TestPostgresTextArrayScansEscapedChannelIDs(t *testing.T) {
	var array postgresTextArray
	if err := array.Scan(`{"channel-one","channel,with,commas","channel\\slash"}`); err != nil {
		t.Fatal(err)
	}
	want := []string{"channel-one", "channel,with,commas", `channel\slash`}
	if !reflect.DeepEqual([]string(array), want) {
		t.Fatalf("array = %#v, want %#v", array, want)
	}
}
