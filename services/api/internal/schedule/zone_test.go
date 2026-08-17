package schedule

import (
	"testing"
	"time"
)

// Le forme leggibili di wallClock e resolution compaiono nei messaggi di
// fallimento dei test e nei log diagnostici: se cambiano in silenzio, quei
// messaggi diventano illeggibili proprio quando servono.
func TestFormeLeggibiliDelRisolutore(t *testing.T) {
	w := wallClock{time.Date(2026, 3, 29, 2, 30, 0, 0, time.UTC)}
	if got, want := w.String(), "2026-03-29 02:30"; got != want {
		t.Errorf("wallClock.String() = %q, atteso %q", got, want)
	}
	casi := map[resolution]string{
		resolutionExact:     "esatto",
		resolutionGap:       "inesistente",
		resolutionAmbiguous: "ambiguo",
	}
	for res, want := range casi {
		if got := res.String(); got != want {
			t.Errorf("resolution(%d).String() = %q, atteso %q", int(res), got, want)
		}
	}
}
