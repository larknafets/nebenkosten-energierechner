package web

import (
	"fmt"

	"github.com/larknafets/nebenkostenrechner/internal/store"
)

// ablesung_validation.go holds the Ablesung input rules shared by both
// input adapters - the Wizard-Formular and the CSV-Import (Issue #86/#87
// follow-up architecture review): same rules, same seam, two callers.

var heizungGewichtungOptions = map[string]float64{"0.7": 0.7, "0.6": 0.6, "0.5": 0.5}

// parseHeizungGewichtung validates the Wizard's Heizung-Gewichtung form
// value against heizungGewichtungOptions.
func parseHeizungGewichtung(raw string) (float64, error) {
	v, ok := heizungGewichtungOptions[raw]
	if !ok {
		return 0, fmt.Errorf("invalid Heizung-Gewichtung %q (muss 0.7, 0.6 oder 0.5 sein)", raw)
	}
	return v, nil
}

// outlierAvg computes the Ausreißer-Warnung baseline (Ticket #13) from up
// to 4 recent periods (newest first): the average of the 3 consumption
// diffs between them. ok is false if fewer than 4 are available.
func outlierAvg(recent []store.PeriodReadings) (avg map[string]float64, ok bool) {
	if len(recent) < 4 {
		return nil, false
	}
	avg = make(map[string]float64, len(store.MeterKeys))
	for _, key := range store.MeterKeys {
		sum := 0.0
		for i := 0; i < 3; i++ {
			sum += recent[i].Readings[key] - recent[i+1].Readings[key]
		}
		avg[key] = sum / 3
	}
	return avg, true
}
