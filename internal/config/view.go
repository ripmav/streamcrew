// SPDX-License-Identifier: MIT

package config

// SecretPlaceholder stands for a set write-only secret in the
// configuration document (Code-ADR-0021, Nachtrag 2026-10-29, point 3):
// the value itself never appears in the document.
const SecretPlaceholder = "<set>"

// FileView is the flat form of a Config with the keys of the configuration
// file. "streamcrew config show" prints it, so its output can be used as a
// configuration file. The overlay token is a credential and stays out,
// like the bootstrap token; the TTS keys are write-only and show the
// placeholder instead of their value (Code-ADR-0021, Nachtrag 2026-10-29,
// point 3).
type FileView struct {
	DataDir           string            `yaml:"data_dir" json:"data_dir"`
	Profile           string            `yaml:"profile,omitempty" json:"profile,omitempty"`
	Mode              Mode              `yaml:"mode" json:"mode"`
	Listen            string            `yaml:"listen" json:"listen"`
	OverlayListen     string            `yaml:"overlay_listen" json:"overlay_listen"`
	Dev               bool              `yaml:"dev" json:"dev"`
	DryRun            bool              `yaml:"dry_run" json:"dry_run"`
	ShutdownTimeout   string            `yaml:"shutdown_timeout" json:"shutdown_timeout"`
	LogLevel          string            `yaml:"log_level" json:"log_level"`
	LogComponentLevel map[string]string `yaml:"log_component_level,omitempty" json:"log_component_level,omitempty"`
	LogFormat         string            `yaml:"log_format" json:"log_format"`
	LogFile           string            `yaml:"log_file" json:"log_file"`
	LogMaxSize        int               `yaml:"log_max_size" json:"log_max_size"`
	LogMaxFiles       int               `yaml:"log_max_files" json:"log_max_files"`
	Grant             []string          `yaml:"grant,omitempty" json:"grant,omitempty"`
	Revoke            []string          `yaml:"revoke,omitempty" json:"revoke,omitempty"`
	FileRoot          map[string]string `yaml:"file_root,omitempty" json:"file_root,omitempty"`
	OutboundAllow     []string          `yaml:"outbound_allow,omitempty" json:"outbound_allow,omitempty"`
	CorsAllow         []string          `yaml:"cors_allow,omitempty" json:"cors_allow,omitempty"`
	TLSCert           string            `yaml:"tls_cert,omitempty" json:"tls_cert,omitempty"`
	TLSKey            string            `yaml:"tls_key,omitempty" json:"tls_key,omitempty"`
	TwitchEventSub    string            `yaml:"twitch_eventsub,omitempty" json:"twitch_eventsub,omitempty"`
	TwitchIRC         string            `yaml:"twitch_irc,omitempty" json:"twitch_irc,omitempty"`
	TTSGoogleKey      string            `yaml:"tts_google_key,omitempty" json:"tts_google_key,omitempty"`
	TTSAzureKey       string            `yaml:"tts_azure_key,omitempty" json:"tts_azure_key,omitempty"`
	TTSAzureRegion    string            `yaml:"tts_azure_region,omitempty" json:"tts_azure_region,omitempty"`
}

// View returns the configuration in the form of the configuration file.
func (c *Config) View() FileView {
	view := FileView{
		DataDir:           c.DataDir,
		Profile:           c.Profile,
		Mode:              c.Mode,
		Listen:            c.Listen,
		OverlayListen:     c.OverlayListen,
		Dev:               c.Dev,
		DryRun:            c.DryRun,
		ShutdownTimeout:   c.ShutdownTimeout.String(),
		LogLevel:          c.Log.Level,
		LogComponentLevel: c.Log.ComponentLevel,
		LogFormat:         c.Log.Format,
		LogFile:           c.Log.File,
		LogMaxSize:        c.Log.MaxSize,
		LogMaxFiles:       c.Log.MaxFiles,
		Grant:             c.Grant,
		Revoke:            c.Revoke,
		FileRoot:          c.FileRoot,
		OutboundAllow:     c.OutboundAllow,
		TwitchEventSub:    c.Twitch.EventSub,
		TwitchIRC:         c.Twitch.IRC,
		TTSAzureRegion:    c.TTSAzureRegion,
	}
	// The write-only secrets show the placeholder, not their value
	// (Code-ADR-0021, Nachtrag 2026-10-29, point 3).
	if c.TTSGoogleKey != "" {
		view.TTSGoogleKey = SecretPlaceholder
	}
	if c.TTSAzureKey != "" {
		view.TTSAzureKey = SecretPlaceholder
	}
	return view
}
