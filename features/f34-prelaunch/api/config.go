package prelaunch

type ModeSource string

const (
	ModeExplicitTrue         ModeSource = "explicit_true"
	ModeExplicitFalse        ModeSource = "explicit_false"
	ModeFailClosed           ModeSource = "fail_closed"
	ModeNonProductionDefault ModeSource = "non_production_default"
)

type Mode struct {
	Enabled bool       `json:"enabled"`
	Source  ModeSource `json:"source"`
}

func ResolveMode(rawValue, environment string) Mode {
	switch rawValue {
	case "true":
		return Mode{Enabled: true, Source: ModeExplicitTrue}
	case "false":
		return Mode{Enabled: false, Source: ModeExplicitFalse}
	default:
		switch environment {
		case "development", "local", "test":
			return Mode{
				Enabled: false,
				Source:  ModeNonProductionDefault,
			}
		default:
			return Mode{Enabled: true, Source: ModeFailClosed}
		}
	}
}
