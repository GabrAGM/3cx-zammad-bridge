package zammadbridge

import (
	"fmt"
	"os"

	"github.com/rs/zerolog/log"
	"gopkg.in/yaml.v2"
)

type Config struct {
	Bridge struct {
		PollInterval float64 `yaml:"poll_interval"`
	} `yaml:"Bridge"`
	Phone3CX struct {
		User            string `yaml:"user"`
		Pass            string `yaml:"pass"`
		ClientID        string `yaml:"client_id"`
		ClientSecret    string `yaml:"client_secret"`
		Host            string `yaml:"host"`
		Group           string `yaml:"group"`
		ExtensionDigits int    `yaml:"extension_digits"`
		TrunkDigits     int    `yaml:"trunk_digits"`
		QueueExtension  int    `yaml:"queue_extension"`
		CountryPrefix   string `yaml:"country_prefix"`
	} `yaml:"3CX"`
	Admin struct {
		// Enabled toggles the self-serve admin web UI. When false (default) no
		// HTTP listener is started.
		Enabled bool `yaml:"enabled"`
		// Listen is the bind address (e.g. ":8090"). Default ":8090" when
		// Enabled=true but Listen is empty.
		Listen string `yaml:"listen"`
		// User / Pass gate the admin UI via HTTP Basic Auth. Required when
		// Enabled=true — the server refuses to start otherwise.
		User string `yaml:"user"`
		Pass string `yaml:"pass"`
	} `yaml:"Admin"`
	Zammad struct {
		Endpoint            string `yaml:"endpoint"`
		LogMissedQueueCalls bool   `yaml:"log_missed_queue_calls"`
		ApiUrl              string `yaml:"api_url"`
		ApiToken            string `yaml:"api_token"`
		AutoCreateTicket    bool   `yaml:"auto_create_ticket"`
		TicketGroup         string `yaml:"ticket_group"`
		// AutoCreateDirections controls which call directions trigger auto-creation
		// of tickets (and users). Accepted values: "all", "inbound", "outbound", "none".
		// Empty string defaults to "all" for backward compatibility.
		AutoCreateDirections string `yaml:"auto_create_directions"`
		// ExtensionFilterMode controls whether the extension filter list acts as an
		// allow-list or deny-list. Accepted values: "all" (no filter), "include", "exclude".
		// Empty string defaults to "all".
		ExtensionFilterMode string `yaml:"extension_filter_mode"`
		// ExtensionFilter lists the 3CX extensions (agent numbers) that the filter
		// mode applies to. Ignored when mode is "all".
		ExtensionFilter []string `yaml:"extension_filter"`
	} `yaml:"Zammad"`
}

// LoadedConfigPath holds the filesystem path of the YAML file that was
// successfully loaded. The admin UI writes back to the same path on save.
var LoadedConfigPath string

// LoadConfigFromYaml tries the provided files for a valid YAML configuration file.
// It uses the first file it can parse, and only that file.
func LoadConfigFromYaml(filenames ...string) (*Config, error) {
	config := new(Config)

	for _, f := range filenames {
		b, err := os.ReadFile(f)
		if err != nil {
			continue // hopefully other files will work out?
		}

		err = yaml.Unmarshal(b, config)
		if err != nil {
			log.Warn().Err(err).Str("file", f).Msg("Unable to parse YAML config")
			continue // hopefully other files will work out?
		}

		LoadedConfigPath = f
		return config, nil
	}

	return nil, fmt.Errorf("unable to find configuration files")
}
