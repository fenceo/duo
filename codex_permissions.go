package main

// Sandbox boundaries and approval routing are independent. "never" refuses
// to ask; it does not grant permission to escape a sandbox. Make every setting
// explicit so global CLI configuration cannot silently expand permissions.
func codexPermissionSettings(t Task) (sandbox, approval, reviewer string, network bool) {
	sandbox, approval, reviewer, network = "workspace-write", "on-request", "user", true
	if t.Mode == nil {
		return
	}
	switch t.Mode.Permission {
	case "read":
		return "read-only", "never", "user", false
	case "full":
		return "danger-full-access", "never", "user", true
	}
	if t.Mode.AllowNetwork != nil {
		network = *t.Mode.AllowNetwork
	}
	switch t.Mode.Approval {
	case "auto":
		reviewer = "auto_review"
	case "never":
		approval = "never"
	}
	return
}
