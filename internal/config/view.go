// SPDX-License-Identifier: Apache-2.0

package config

// FileView is the flat form of a Config with the keys of the configuration
// file. "streamcrew config show" prints it, so its output can be used as a
// configuration file.
type FileView struct {
	DataDir           string            `yaml:"data_dir" json:"data_dir"`
	Profile           string            `yaml:"profile,omitempty" json:"profile,omitempty"`
	Mode              Mode              `yaml:"mode" json:"mode"`
	Listen            string            `yaml:"listen" json:"listen"`
	Dev               bool              `yaml:"dev" json:"dev"`
	ShutdownTimeout   string            `yaml:"shutdown_timeout" json:"shutdown_timeout"`
	LogLevel          string            `yaml:"log_level" json:"log_level"`
	LogComponentLevel map[string]string `yaml:"log_component_level,omitempty" json:"log_component_level,omitempty"`
	LogFormat         string            `yaml:"log_format" json:"log_format"`
	LogFile           bool              `yaml:"log_file" json:"log_file"`
	LogMaxSize        int               `yaml:"log_max_size" json:"log_max_size"`
	LogMaxFiles       int               `yaml:"log_max_files" json:"log_max_files"`
	Grant             []string          `yaml:"grant,omitempty" json:"grant,omitempty"`
	Revoke            []string          `yaml:"revoke,omitempty" json:"revoke,omitempty"`
	FileRoot          map[string]string `yaml:"file_root,omitempty" json:"file_root,omitempty"`
	OutboundAllow     []string          `yaml:"outbound_allow,omitempty" json:"outbound_allow,omitempty"`
}

// View returns the configuration in the form of the configuration file.
func (c *Config) View() FileView {
	return FileView{
		DataDir:           c.DataDir,
		Profile:           c.Profile,
		Mode:              c.Mode,
		Listen:            c.Listen,
		Dev:               c.Dev,
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
	}
}
