package operations

import "time"

// OperationalPolicy mirrors the adopted D05 operational objectives. Keeping
// these values in code makes alert and recovery configuration testable.
type OperationalPolicy struct {
	OperationalLogRetention time.Duration
	AuditRetentionMonths    int
	MetricRetentionMonths   int
	BackupRetention         time.Duration
	DatabaseRPO             time.Duration
	DatabaseRTO             time.Duration
	ObjectRPO               time.Duration
	ObjectRTO               time.Duration
	EndToEndRTO             time.Duration
	BackupMaxAge            time.Duration
	RestoreTestMaxAge       time.Duration
}

func DefaultOperationalPolicy() OperationalPolicy {
	const day = 24 * time.Hour
	return OperationalPolicy{
		OperationalLogRetention: 30 * day,
		AuditRetentionMonths:    12,
		MetricRetentionMonths:   13,
		BackupRetention:         35 * day,
		DatabaseRPO:             15 * time.Minute,
		DatabaseRTO:             4 * time.Hour,
		ObjectRPO:               day,
		ObjectRTO:               8 * time.Hour,
		EndToEndRTO:             8 * time.Hour,
		BackupMaxAge:            day,
		RestoreTestMaxAge:       31 * day,
	}
}
