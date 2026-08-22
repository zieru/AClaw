package whatsapp

// WhatsAppSettings holds channel-specific configurations for WhatsApp Native
type WhatsAppSettings struct {
	JID            string   `json:"jid"`             // e.g. "628123456789@s.whatsapp.net"
	PushName       string   `json:"push_name"`       // Connected account name
	DMPolicy       string   `json:"dm_policy"`       // "allow" (default), "trusted", "block"
	TrustedNumbers []string `json:"trusted_numbers"` // List of phone numbers / JID strings
	GroupPolicy    string   `json:"group_policy"`    // "allow_all" (default), "whitelist", "block"
	AllowedGroups  []string `json:"allowed_groups"`  // List of group JIDs e.g. "120363xxx@g.us"
	MentionPolicy  string   `json:"mention_policy"`  // "require_mention" (default), "all"
}

const (
	DMPolicyAllow   = "allow"
	DMPolicyTrusted = "trusted"
	DMPolicyBlock   = "block"

	GroupPolicyAllowAll  = "allow_all"
	GroupPolicyWhitelist = "whitelist"
	GroupPolicyBlock     = "block"

	MentionPolicyRequire = "require_mention"
	MentionPolicyAll     = "all"
)

// DefaultWhatsAppSettings returns default settings for new channels
func DefaultWhatsAppSettings() WhatsAppSettings {
	return WhatsAppSettings{
		DMPolicy:       DMPolicyAllow,
		GroupPolicy:    GroupPolicyAllowAll,
		MentionPolicy:  MentionPolicyRequire,
		TrustedNumbers: []string{},
		AllowedGroups:  []string{},
	}
}
