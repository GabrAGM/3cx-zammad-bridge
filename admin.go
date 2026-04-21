package zammadbridge

import (
	"crypto/subtle"
	"fmt"
	"html/template"
	"net/http"
	"os"
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
select[multiple] { min-height: 260px; font-family: monospace; }

.shuttle { display: grid; grid-template-columns: 1fr auto 1fr; gap: .6rem; align-items: stretch; margin-top: .3rem; }
.shuttle .pane { display: flex; flex-direction: column; gap: .3rem; min-width: 0; }
.shuttle .pane input[type=text] { margin: 0; }
.shuttle .pane select { flex: 1; min-height: 260px; width: 100%; box-sizing: border-box; }
.shuttle .pane-title { font-size: .85rem; font-weight: 600; color: #555; }
.shuttle-buttons { display: flex; flex-direction: column; justify-content: center; gap: .3rem; }
.shuttle-buttons button { margin: 0; padding: .3rem .6rem; font-size: .85rem; background: #eef1f4; color: #222; border: 1px solid #d1d5da; }
.shuttle-buttons button:hover { background: #d1d5da; }
.pane-count { font-size: .75rem; color: #888; }
.inline-mode { width: auto; display: inline-block; font-size: .85rem; padding: 2px 6px; margin: 0 .2rem; font-weight: 600; }
.direction-toggles { margin-top: .3rem; }
.toggle-row { padding: .4rem 0; }
.switch-label { display: inline-flex; align-items: center; gap: .5rem; cursor: pointer; font-weight: normal; }
.switch-label input[type=checkbox] { width: 38px; height: 22px; appearance: none; background: #cbd1d8; border-radius: 11px; position: relative; cursor: pointer; transition: background .15s; outline: none; border: none; margin: 0; }
.switch-label input[type=checkbox]:checked { background: #2ea043; }
.switch-label input[type=checkbox]::before { content: ""; position: absolute; left: 2px; top: 2px; width: 18px; height: 18px; background: #fff; border-radius: 50%; transition: left .15s; box-shadow: 0 1px 2px rgba(0,0,0,.2); }
.switch-label input[type=checkbox]:checked::before { left: 18px; }
.switch-text { font-size: .95rem; }
</style>
<script>
function shuttleFilter(id, q) {
  q = q.trim().toLowerCase();
  const sel = document.getElementById(id);
  for (const o of sel.options) {
    o.hidden = q && !o.textContent.toLowerCase().includes(q);
  }
}
function shuttleMove(fromId, toId, all) {
  const from = document.getElementById(fromId);
  const to = document.getElementById(toId);
  const moving = [];
  for (const o of Array.from(from.options)) {
    if (o.hidden) continue;
    if (all || o.selected) moving.push(o);
  }
  for (const o of moving) {
    o.selected = false;
    to.appendChild(o);
  }
  sortSelect(to);
  updateShuttleCounts();
}
function sortSelect(sel) {
  const opts = Array.from(sel.options);
  opts.sort((a, b) => {
    const na = parseInt(a.value, 10), nb = parseInt(b.value, 10);
    if (!isNaN(na) && !isNaN(nb) && na !== nb) return na - nb;
    return a.value.localeCompare(b.value);
  });
  opts.forEach(o => sel.appendChild(o));
}
function updateShuttleCounts() {
  const a = document.getElementById('available-select');
  const s = document.getElementById('selected-select');
  const ac = document.getElementById('available-count');
  const sc = document.getElementById('selected-count');
  if (a && ac) ac.textContent = a.options.length + ' extensions';
  if (s && sc) sc.textContent = s.options.length + ' selected';
}
function selectAllInSelected() {
  const s = document.getElementById('selected-select');
  if (!s) return;
  for (const o of s.options) o.selected = true;
}
function updateModeLabel() {
  const modeSel = document.querySelector('select[name="extension_filter_mode"]');
  const label = document.getElementById('selected-label');
  if (!modeSel || !label) return;
  const m = modeSel.value;
  if (m === 'include') {
    label.textContent = 'Only calls on the extensions listed here will auto-create a ticket — everything else is ignored.';
  } else if (m === 'exclude') {
    label.textContent = 'Calls on the extensions listed here are skipped; every other extension creates tickets normally.';
  } else {
    label.textContent = 'Filter is currently off — every extension creates tickets.';
  }
}
function initAdminUI() {
  updateShuttleCounts();
  updateModeLabel();
  const modeSel = document.querySelector('select[name="extension_filter_mode"]');
  if (modeSel) modeSel.addEventListener('change', updateModeLabel);
  const form = document.querySelector('form');
  if (form) form.addEventListener('submit', selectAllInSelected);
}
if (document.readyState === 'loading') {
  document.addEventListener('DOMContentLoaded', initAdminUI);
} else {
  // Document is already parsed (script may have been injected late) — init now.
  initAdminUI();
}
</script>
</head>
<body>
<h1>3CX → Zammad bridge <small>auto-create settings</small></h1>
<p class="hint">Changes are applied instantly — no restart, no dropped calls. The config file on disk is also updated so settings survive container restarts.</p>

{{if .Message}}<div class="banner {{.MessageKind}}">{{.Message}}</div>{{end}}

<form method="POST" action="/save">
  <div class="card">
    <div class="direction-toggles">
      <div class="toggle-row">
        <label class="switch-label">
          <input type="checkbox" name="inbound" value="on" {{if .InboundOn}}checked{{end}}>
          <span class="switch-text">Create tickets for <b>Inbound</b> calls</span>
        </label>
      </div>
      <div class="toggle-row">
        <label class="switch-label">
          <input type="checkbox" name="outbound" value="on" {{if .OutboundOn}}checked{{end}}>
          <span class="switch-text">Create tickets for <b>Outbound</b> calls</span>
        </label>
      </div>
      <div class="hint">Untick both to fully stop ticket creation — the bridge still forwards live CTI events to Zammad but never touches the /tickets endpoint.</div>
    </div>

    <label>Extensions</label>
    {{if .ExtensionsError}}<div class="hint" style="color:#86181d">Could not load 3CX extension directory ({{.ExtensionsError}}) — using the numbers that are already on file.</div>{{end}}
    {{if .Extensions}}
      <div class="shuttle">
        <div class="pane">
          <div class="pane-title">Available <span class="pane-count" id="available-count"></span></div>
          <input type="text" placeholder="Search by number or name…" oninput="shuttleFilter('available-select', this.value)">
          <select id="available-select" multiple ondblclick="shuttleMove('available-select','selected-select')">
            {{range .Extensions}}{{if not (index $.ExtListMap .Number)}}
            <option value="{{.Number}}">{{.Number}} — {{.Name}}</option>
            {{end}}{{end}}
          </select>
        </div>
        <div class="shuttle-buttons">
          <button type="button" onclick="shuttleMove('available-select','selected-select')" title="Move selected rows to the filter list">→</button>
          <button type="button" onclick="shuttleMove('selected-select','available-select')" title="Move selected rows back to Available">←</button>
          <button type="button" onclick="shuttleMove('available-select','selected-select', true)" title="Move all visible rows to the filter list">⇒</button>
          <button type="button" onclick="shuttleMove('selected-select','available-select', true)" title="Clear the filter list">⇐</button>
        </div>
        <div class="pane">
          <div class="pane-title">
            Extensions that are
            <select name="extension_filter_mode" class="inline-mode">
              <option value="exclude" {{if eq .ExtMode "exclude"}}selected{{end}}>Excluded</option>
              <option value="include" {{if eq .ExtMode "include"}}selected{{end}}>Included</option>
              <option value="all"     {{if eq .ExtMode "all"}}selected{{end}}>Ignored (filter off)</option>
            </select>
            <span class="pane-count" id="selected-count"></span>
          </div>
          <input type="text" placeholder="Search selected…" oninput="shuttleFilter('selected-select', this.value)">
          <select id="selected-select" name="extension_filter" multiple ondblclick="shuttleMove('selected-select','available-select')">
            {{range .Extensions}}{{if index $.ExtListMap .Number}}
            <option value="{{.Number}}">{{.Number}} — {{.Name}}</option>
            {{end}}{{end}}
          </select>
          <div class="hint" id="selected-label"></div>
        </div>
      </div>
      <div class="hint">Double-click a row to move it across. The filter mode above controls whether the right-hand list is treated as an include-list or an exclude-list.</div>
    {{else}}
      <textarea name="extension_filter" placeholder="908&#10;909&#10;910">{{.ExtList}}</textarea>
      <div class="hint">One per line. Directory lookup from 3CX was not available, so you're editing the list directly.</div>
    {{end}}
  </div>

  <button type="submit">Save &amp; apply</button>
</form>

<div class="current">
  Config file on server: <code>{{.ConfigPath}}</code>
</div>
</body>
</html>`

type adminView struct {
	InboundOn       bool
	OutboundOn      bool
	ExtMode         string
	ExtList         string          // newline-separated — used only by textarea fallback
	ExtListMap      map[string]bool // numbers currently in filter, used by multi-select
	Extensions      []Extension     // from 3CX directory
	ExtensionsError string          // set when directory fetch failed
	ConfigPath      string
	Message         string
	MessageKind     string
}

func viewFromSettings(s AutoCreateSettings, extensions []Extension, extensionsErr error, configPath, message, kind string) adminView {
	dir := strings.ToLower(strings.TrimSpace(s.Directions))
	if dir == "" {
		dir = "all"
	}
	inbound := s.Enabled && (dir == "all" || dir == "inbound" || dir == "both" || dir == "in")
	outbound := s.Enabled && (dir == "all" || dir == "outbound" || dir == "both" || dir == "out")
	mode := strings.ToLower(strings.TrimSpace(s.ExtMode))
	if mode == "" {
		mode = "all"
	}
	selected := make(map[string]bool, len(s.ExtList))
	for _, e := range s.ExtList {
		selected[e] = true
	}
	// If a currently-filtered number isn't in the directory (e.g. 3CX
	// returned an error or the extension was removed from 3CX), synthesize
	// a placeholder option so it stays visible + selectable.
	if len(extensions) > 0 {
		known := make(map[string]bool, len(extensions))
		for _, e := range extensions {
			known[e.Number] = true
		}
		for _, e := range s.ExtList {
			if !known[e] {
				extensions = append(extensions, Extension{Number: e, Name: "(not in directory)"})
			}
		}
	}
	errStr := ""
	if extensionsErr != nil {
		errStr = extensionsErr.Error()
	}
	return adminView{
		InboundOn:       inbound,
		OutboundOn:      outbound,
		ExtMode:         mode,
		ExtList:         strings.Join(s.ExtList, "\n"),
		ExtListMap:      selected,
		Extensions:      extensions,
		ExtensionsError: errStr,
		ConfigPath:      configPath,
		Message:         message,
		MessageKind:     kind,
	}
}

func adminIndexHandler(bridge *ZammadBridge, configPath string) http.HandlerFunc {
	tmpl := template.Must(template.New("admin").Parse(adminTmpl))
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" && r.URL.Path != "" {
			http.NotFound(w, r)
			return
		}
		extensions, extErr := bridge.GetExtensions()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = tmpl.Execute(w, viewFromSettings(bridge.GetAutoCreateSettings(), extensions, extErr, configPath, "", ""))
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

		// Multi-select submits one entry per picked option; textarea
		// fallback submits a single entry with embedded newlines. Both
		// shapes parse through parseExtList.
		rawExts := r.Form["extension_filter"]
		var extList []string
		if len(rawExts) > 1 {
			for _, e := range rawExts {
				if v := strings.TrimSpace(e); v != "" {
					extList = append(extList, v)
				}
			}
		} else if len(rawExts) == 1 {
			extList = parseExtList(rawExts[0])
		}

		inboundOn := r.FormValue("inbound") != ""
		outboundOn := r.FormValue("outbound") != ""

		var directions string
		switch {
		case inboundOn && outboundOn:
			directions = "all"
		case inboundOn:
			directions = "inbound"
		case outboundOn:
			directions = "outbound"
		default:
			directions = "none"
		}

		newSettings := AutoCreateSettings{
			// The master "Enabled" flag mirrors "any direction is on". If both
			// toggles are off, no call can auto-create, so we also record
			// Enabled=false so the log line + persisted YAML match intent.
			Enabled:    inboundOn || outboundOn,
			Directions: directions,
			ExtMode:    strings.ToLower(strings.TrimSpace(r.FormValue("extension_filter_mode"))),
			ExtList:    extList,
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
		extensions, extErr := bridge.GetExtensions()
		_ = tmpl.Execute(w, viewFromSettings(bridge.GetAutoCreateSettings(), extensions, extErr, configPath,
			"Saved. New settings are active now — future calls will use them.", "success"))
	}
}

func writeError(w http.ResponseWriter, tmpl *template.Template, bridge *ZammadBridge, configPath, msg string) {
	extensions, extErr := bridge.GetExtensions()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusBadRequest)
	_ = tmpl.Execute(w, viewFromSettings(bridge.GetAutoCreateSettings(), extensions, extErr, configPath, msg, "error"))
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

// writeConfigYAML marshals the config back to YAML and writes it to the
// given path. We cannot use the usual write+rename atomic pattern because
// the config file is typically bind-mounted into the container as a single
// file — Docker's mount holds the inode and rename(2) fails with EBUSY.
// Instead, we open the existing file O_TRUNC and write in place. For a
// small YAML (~1 KB) this is near-instantaneous; the narrow crash window
// is acceptable for this use case.
func writeConfigYAML(path string, cfg *Config) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	return nil
}
