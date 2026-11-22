package main

// AppLimits bounds how much data a single app may hold. The zero value of any
// field means "unlimited" for that dimension, so an operator-provisioned app
// (which stores no limits) is unbounded, exactly as before.
type AppLimits struct {
	MaxUsers               int `bson:"maxUsers" json:"maxUsers"`
	MaxRecordsPerNamespace int `bson:"maxRecordsPerNamespace" json:"maxRecordsPerNamespace"`
	MaxRecordBytes         int `bson:"maxRecordBytes" json:"maxRecordBytes"`
}

// sandboxLimits are the tight, fixed quotas every ephemeral tenant carries so a
// public share link can never be turned into unbounded storage.
func sandboxLimits() AppLimits {
	return AppLimits{
		MaxUsers:               vibeSandboxMaxUsers,
		MaxRecordsPerNamespace: vibeSandboxMaxResources,
		MaxRecordBytes:         vibeSandboxMaxRecordBytes,
	}
}

// freeTierLimits are stamped onto apps claimed by a self-serve (free-plan)
// builder, keeping a hosted deployment's free tier bounded.
func freeTierLimits(cfg Config) AppLimits {
	return AppLimits{
		MaxUsers:               cfg.FreeTierMaxUsers,
		MaxRecordsPerNamespace: cfg.FreeTierMaxRecords,
		MaxRecordBytes:         cfg.FreeTierMaxRecordBytes,
	}
}

// freeTierLimitsFromSettings stamps the free-tier quotas from the live, resolved
// settings so an operator's runtime quota change applies to newly created and
// newly claimed apps immediately.
func freeTierLimitsFromSettings(settings resolvedSettings) AppLimits {
	return AppLimits{
		MaxUsers:               settings.FreeTierMaxUsers,
		MaxRecordsPerNamespace: settings.FreeTierMaxRecords,
		MaxRecordBytes:         settings.FreeTierMaxRecordBytes,
	}
}

// effectiveLimits resolves the limits that apply to a record right now. A stored
// Limits value always wins; otherwise a sandbox falls back to sandboxLimits so
// legacy sandbox documents (written before Limits existed) need no migration,
// and every other app is unbounded.
func effectiveLimits(record *AppRecord) AppLimits {
	if record == nil {
		return AppLimits{}
	}
	if record.Limits != nil {
		return *record.Limits
	}
	if record.Sandbox {
		return sandboxLimits()
	}
	return AppLimits{}
}
