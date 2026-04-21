package zammadbridge

import (
	"crypto/subtle"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/rs/zerolog/log"
	"gopkg.in/yaml.v2"
)

// StartAdminServer runs a tiny self-serve admin UI exposing the auto-create
// direction + extension filter on the live config file. Blocks; run in a
// goroutine. Returns and logs a skip message if the admin UI is disabled or
// misconfigured — never blocks the bridge from starting.
//
// Saves are applied hot: the in-memory bridge picks up the new settings on
// the next call, and the YAML file on disk is rewritten so the settings
// persist across container restarts.
func StartAdminServer(bridge *ZammadBridge, configPath string) {
	if bridge == nil {
		log.Warn().Msg("Admin UI: nil bridge — refusing to start")
		return
	}
	cfg := bridge.Config
	if !cfg.Admin.Enabled {
		log.Info().Msg("Admin UI disabled (Admin.enabled=false in config)")
		return
	}
	if cfg.Admin.User == "" || cfg.Admin.Pass == "" {
		log.Warn().Msg("Admin UI enabled but user/pass empty — refusing to start")
		return
	}
	if configPath == "" {
		log.Warn().Msg("Admin UI enabled but loaded config path is unknown — refusing to start")
		return
	}

	listen := cfg.Admin.Listen
	if listen == "" {
		listen = ":8090"
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", basicAuth(cfg, adminIndexHandler(bridge, configPath)))
	mux.HandleFunc("/save", basicAuth(cfg, adminSaveHandler(bridge, configPath)))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})

	log.Info().Str("listen", listen).Msg("Admin UI listening")
	if err := http.ListenAndServe(listen, mux); err != nil {
		log.Error().Err(err).Msg("Admin UI stopped")
	}
}

func basicAuth(cfg *Config, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		userOK := subtle.ConstantTimeCompare([]byte(user), []byte(cfg.Admin.User)) == 1
		passOK := subtle.ConstantTimeCompare([]byte(pass), []byte(cfg.Admin.Pass)) == 1
		if !ok || !userOK || !passOK {
			w.Header().Set("WWW-Authenticate", `Basic realm="3cx-zammad-bridge"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

const adminTmpl = `<!doctype html>
<html>
<head>
<meta charset="utf-8">
<title>3CX → Zammad bridge — auto-create settings</title>
<style>
body { font-family: -apple-system, Segoe UI, Roboto, sans-serif; max-width: 720px; margin: 2rem auto; padding: 0 1rem; color: #222; }
h1 { font-size: 1.4rem; margin-bottom: .2rem; }
h1 small { color: #888; font-weight: normal; font-size: .8rem; }
.card { border: 1px solid #e1e4e8; border-radius: 6px; padding: 1rem 1.2rem; margin-top: 1rem; background: #fafbfc; }
label { display: block; margin-top: .8rem; font-weight: 600; }
.hint { color: #666; font-weight: normal; font-size: .85rem; margin-top: .2rem; }
select, textarea, input[type=text] { width: 100%; padding: .4rem .5rem; font-size: .95rem; border: 1px solid #d1d5da; border-radius: 4px; box-sizing: border-box; font-family: inherit; }
textarea { min-height: 120px; font-family: monospace; }
button { margin-top: 1.2rem; padding: .5rem 1.2rem; font-size: 1rem; background: #0366d6; color: #fff; border: none; border-radius: 4px; cursor: pointer; }
button:hover { background: #0256c7; }
.banner { padding: .6rem .9rem; border-radius: 4px; margin-bottom: 1rem; }
.banner.success { background: #d1f5d3; color: #11591c; }
.banner.error { background: #ffd7d7; color: #86181d; }
code { background: #eef1f4; padding: 1px 5px; border-radius: 3px; font-size: .85em; }
.current { font-size: .85rem; color: #555; margin-top: .3rem; }
</style>
</head>
<body>
<h1>3CX → Zammad bridge <small>auto-create settings</small></h1>
<p class="hint">Changes are applied instantly — no restart, no dropped calls. The config file on disk is also updated so settings survive container restarts.</p>

{{if .Message}}<div class="banner {{.MessageKind}}">{{.Message}}</div>{{end}}

<form method="POST" action="/save">
  <div class="card">
    <label>Master toggle
      <select name="auto_create_ticket">
        <option value="true"  {{if .AutoCreateTicket}}selected{{end}}>Enabled — create Zammad tickets on hangup</option>
        <option value="false" {{if not .AutoCreateTicket}}selected{{end}}>Disabled — do not create tickets at all</option>
      </select>
      <div class="hint">When disabled, the bridge still forwards live CTI events to Zammad but never touches the /tickets endpoint.</div>
    </label>

    <label>Call directions to auto-create for
      <select name="auto_create_directions">
        <option value="all"      {{if eq .Directions "all"}}selected{{end}}>All — inbound and outbound</option>
        <option value="inbound"  {{if eq .Directions "inbound"}}selected{{end}}>Inbound only</option>
        <option value="outbound" {{if eq .Directions "outbound"}}selected{{end}}>Outbound only</option>
        <option value="none"     {{if eq .Directions "none"}}selected{{end}}>None — skip every call</option>
      </select>
    </label>

    <label>Extension filter mode
      <select name="extension_filter_mode">
        <option value="all"     {{if eq .ExtMode "all"}}selected{{end}}>All — no filter, every agent extension</option>
        <option value="include" {{if eq .ExtMode "include"}}selected{{end}}>Include — only the extensions listed below</option>
        <option value="exclude" {{if eq .ExtMode "exclude"}}selected{{end}}>Exclude — every extension EXCEPT those listed</option>
      </select>
    </label>

    <label>Extensions (one per line)
      <textarea name="extension_filter" placeholder="908&#10;909&#10;910">{{.ExtList}}</textarea>
      <div class="hint">Ignored when mode is "All". Match is on the agent side of the call (the 3CX extension that answered an inbound or placed an outbound).</div>
    </label>
  </div>

  <button type="submit">Save &amp; apply</button>
</form>

<div class="current">
  Config file on server: <code>{{.ConfigPath}}</code>
</div>
</body>
</html>`

type adminView struct {
	AutoCreateTicket bool
	Directions       string
	ExtMode          string
	ExtList          string
	ConfigPath       string
	Message          string
	MessageKind      string
}

func viewFromSettings(s AutoCreateSettings, configPath, message, kind string) adminView {
	dir := strings.ToLower(strings.TrimSpace(s.Directions))
	if dir == "" {
		dir = "all"
	}
	mode := strings.ToLower(strings.TrimSpace(s.ExtMode))
	if mode == "" {
		mode = "all"
	}
	return adminView{
		AutoCreateTicket: s.Enabled,
		Directions:       dir,
		ExtMode:          mode,
		ExtList:          strings.Join(s.ExtList, "\n"),
		ConfigPath:       configPath,
		Message:          message,
		MessageKind:      kind,
	}
}

func adminIndexHandler(bridge *ZammadBridge, configPath string) http.HandlerFunc {
	tmpl := template.Must(template.New("admin").Parse(adminTmpl))
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" && r.URL.Path != "" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = tmpl.Execute(w, viewFromSettings(bridge.GetAutoCreateSettings(), configPath, "", ""))
	}
}

func adminSaveHandler(bridge *ZammadBridge, configPath string) http.HandlerFunc {
	tmpl := template.Must(template.New("admin").Parse(adminTmpl))
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form: "+err.Error(), http.StatusBadRequest)
			return
		}

		newSettings := AutoCreateSettings{
			Enabled:    r.FormValue("auto_create_ticket") == "true",
			Directions: strings.ToLower(strings.TrimSpace(r.FormValue("auto_create_directions"))),
			ExtMode:    strings.ToLower(strings.TrimSpace(r.FormValue("extension_filter_mode"))),
			ExtList:    parseExtList(r.FormValue("extension_filter")),
		}

		if !validDirection(newSettings.Directions) {
			writeError(w, tmpl, bridge, configPath, "Invalid direction value: "+newSettings.Directions)
			return
		}
		if !validExtMode(newSettings.ExtMode) {
			writeError(w, tmpl, bridge, configPath, "Invalid extension filter mode: "+newSettings.ExtMode)
			return
		}

		// Persist to disk first so a crash after the in-memory swap doesn't
		// silently lose the change.
		fileCfg, err := LoadConfigFromYaml(configPath)
		if err != nil {
			writeError(w, tmpl, bridge, configPath, "Could not read current config: "+err.Error())
			return
		}
		fileCfg.Zammad.AutoCreateTicket = newSettings.Enabled
		fileCfg.Zammad.AutoCreateDirections = newSettings.Directions
		fileCfg.Zammad.ExtensionFilterMode = newSettings.ExtMode
		fileCfg.Zammad.ExtensionFilter = newSettings.ExtList
		if err := writeConfigYAML(configPath, fileCfg); err != nil {
			writeError(w, tmpl, bridge, configPath, "Could not write config: "+err.Error())
			return
		}

		// Hot-swap in-memory — the next hangup picks up these values.
		bridge.SetAutoCreateSettings(newSettings)

		log.Info().
			Bool("auto_create", newSettings.Enabled).
			Str("directions", newSettings.Directions).
			Str("ext_mode", newSettings.ExtMode).
			Int("ext_count", len(newSettings.ExtList)).
			Str("changed_by", basicAuthUser(r)).
			Msg("Admin UI applied new auto-create settings")

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = tmpl.Execute(w, viewFromSettings(bridge.GetAutoCreateSettings(), configPath,
			"Saved. New settings are active now — future calls will use them.", "success"))
	}
}

func writeError(w http.ResponseWriter, tmpl *template.Template, bridge *ZammadBridge, configPath, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusBadRequest)
	_ = tmpl.Execute(w, viewFromSettings(bridge.GetAutoCreateSettings(), configPath, msg, "error"))
}

func basicAuthUser(r *http.Request) string {
	u, _, _ := r.BasicAuth()
	return u
}

func validDirection(d string) bool {
	switch d {
	case "all", "inbound", "outbound", "none":
		return true
	}
	return false
}

func validExtMode(m string) bool {
	switch m {
	case "all", "include", "exclude":
		return true
	}
	return false
}

func parseExtList(raw string) []string {
	out := []string{}
	for _, line := range strings.Split(raw, "\n") {
		ext := strings.TrimSpace(line)
		if ext == "" {
			continue
		}
		out = append(out, ext)
	}
	return out
}

// writeConfigYAML marshals the config back to YAML and writes it atomically
// to the given path (write + rename, same directory) so a crashing process
// cannot leave a truncated file.
func writeConfigYAML(path string, cfg *Config) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".config.yaml.*.tmp")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("write temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("close temp: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}
