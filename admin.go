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
*, *::before, *::after { box-sizing: border-box; }
body { font-family: -apple-system, Segoe UI, Roboto, sans-serif; max-width: 980px; margin: 2rem auto; padding: 0 1.5rem; color: #222; background: #f6f8fa; }
h1 { font-size: 1.4rem; margin: 0 0 .2rem; }
h1 small { color: #888; font-weight: normal; font-size: .8rem; }
.page-hint { color: #666; font-size: .85rem; margin: 0 0 1.2rem; }
.hint { color: #666; font-weight: normal; font-size: .82rem; margin-top: .3rem; line-height: 1.4; }
.section { background: #fff; border: 1px solid #e1e4e8; border-radius: 8px; padding: 1.2rem 1.4rem; margin-bottom: 1rem; }
.section-title { font-size: .8rem; font-weight: 700; text-transform: uppercase; letter-spacing: .06em; color: #888; margin: 0 0 .8rem; }
select, textarea, input[type=text] { width: 100%; padding: .4rem .5rem; font-size: .9rem; border: 1px solid #d1d5da; border-radius: 4px; font-family: inherit; background: #fff; }
select:focus, textarea:focus, input[type=text]:focus { outline: none; border-color: #0366d6; box-shadow: 0 0 0 3px rgba(3,102,214,.15); }
textarea { min-height: 120px; font-family: monospace; }
.actions { display: flex; align-items: center; gap: 1rem; margin-top: 1rem; }
button[type=submit] { padding: .5rem 1.4rem; font-size: .95rem; font-weight: 600; background: #0366d6; color: #fff; border: none; border-radius: 6px; cursor: pointer; }
button[type=submit]:hover { background: #0256c7; }
.banner { padding: .6rem .9rem; border-radius: 6px; margin-bottom: 1rem; font-size: .9rem; }
.banner.success { background: #d1f5d3; color: #0a6e1b; border: 1px solid #a8e6b0; }
.banner.error   { background: #ffd7d7; color: #86181d; border: 1px solid #f5c5c5; }
code { background: #eef1f4; padding: 1px 5px; border-radius: 3px; font-size: .82em; }
.footer-meta { font-size: .8rem; color: #888; margin-top: .5rem; }

/* Direction toggles */
.toggle-grid { display: grid; grid-template-columns: 1fr 1fr; gap: .6rem; }
.toggle-row { display: flex; align-items: center; gap: .75rem; padding: .7rem 1rem; border: 1px solid #e1e4e8; border-radius: 6px; background: #fafbfc; }
.toggle-row:has(input:checked) { border-color: #2ea043; background: #f0fdf4; }
.switch-label { display: contents; cursor: pointer; }
.switch-label input[type=checkbox] { flex-shrink: 0; width: 38px; height: 22px; appearance: none; background: #cbd1d8; border-radius: 11px; position: relative; cursor: pointer; transition: background .15s; outline: none; border: none; margin: 0; }
.switch-label input[type=checkbox]:checked { background: #2ea043; }
.switch-label input[type=checkbox]::before { content: ""; position: absolute; left: 2px; top: 2px; width: 18px; height: 18px; background: #fff; border-radius: 50%; transition: left .15s; box-shadow: 0 1px 2px rgba(0,0,0,.2); }
.switch-label input[type=checkbox]:checked::before { left: 18px; }
.switch-text { font-size: .92rem; font-weight: 500; cursor: pointer; }
.toggle-hint { font-size: .8rem; color: #888; margin-top: .6rem; }

/* Shuttle */
.shuttle { display: grid; grid-template-columns: 1fr 36px 1fr; gap: .5rem; align-items: stretch; }
.shuttle .pane { display: flex; flex-direction: column; gap: .3rem; min-width: 0; }
.shuttle .pane input[type=text] { font-size: .85rem; }
.shuttle .pane select[multiple] { flex: 1; min-height: 300px; font-family: monospace; font-size: .82rem; }
.pane-header { display: flex; align-items: baseline; gap: .3rem; flex-wrap: wrap; }
.pane-label { font-size: .8rem; font-weight: 700; text-transform: uppercase; letter-spacing: .05em; color: #555; }
.pane-count { font-size: .75rem; color: #aaa; }
.shuttle-buttons { display: flex; flex-direction: column; justify-content: center; gap: .4rem; }
.shuttle-buttons button { margin: 0; padding: .35rem .5rem; font-size: .85rem; background: #eef1f4; color: #444; border: 1px solid #d1d5da; border-radius: 4px; font-weight: bold; cursor: pointer; }
.shuttle-buttons button:hover { background: #d1d5da; }
.inline-mode { width: auto !important; display: inline-block; font-size: .8rem; padding: 1px 4px; margin: 0 .15rem; font-weight: 600; border-radius: 3px; }
.shuttle-dblclick-hint { font-size: .78rem; color: #aaa; margin-top: .3rem; }
.manual-add { display: flex; gap: .4rem; margin-top: .5rem; align-items: center; }
.manual-add input { width: 110px; flex-shrink: 0; font-family: monospace; }
.manual-add button { margin: 0; padding: .3rem .7rem; font-size: .82rem; background: #eef1f4; color: #444; border: 1px solid #d1d5da; border-radius: 4px; cursor: pointer; white-space: nowrap; }
.manual-add button:hover { background: #d1d5da; }
.manual-add .hint { margin: 0; }
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
function addManualExtension() {
  const input = document.getElementById('manual-ext-input');
  const num = input.value.trim();
  if (!num) return;
  const avail = document.getElementById('available-select');
  const sel = document.getElementById('selected-select');
  for (const o of [...avail.options, ...sel.options]) {
    if (o.value === num) { input.value = ''; input.focus(); return; }
  }
  const opt = new Option(num + ' — (manual entry)', num);
  avail.appendChild(opt);
  sortSelect(avail);
  updateShuttleCounts();
  input.value = '';
  input.focus();
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
<p class="page-hint">Changes are applied instantly — no restart, no dropped calls. The config file on disk is also updated so settings survive container restarts.</p>

{{if .Message}}<div class="banner {{.MessageKind}}">{{.Message}}</div>{{end}}

<form method="POST" action="save">

  <div class="section">
    <div class="section-title">Ticket auto-creation</div>
    <div class="toggle-grid">
      <div class="toggle-row">
        <label class="switch-label">
          <input type="checkbox" name="inbound" value="on" {{if .InboundOn}}checked{{end}}>
          <span class="switch-text">Inbound calls</span>
        </label>
      </div>
      <div class="toggle-row">
        <label class="switch-label">
          <input type="checkbox" name="outbound" value="on" {{if .OutboundOn}}checked{{end}}>
          <span class="switch-text">Outbound calls</span>
        </label>
      </div>
    </div>
    <div class="toggle-hint">Disabling both stops ticket creation entirely — the bridge still forwards live CTI events to Zammad for the agent widget.</div>
  </div>

  <div class="section">
    <div class="section-title">Extension filter</div>
    {{if .ExtensionsError}}<div class="hint" style="color:#86181d;margin-bottom:.5rem">⚠ Could not load 3CX extension directory ({{.ExtensionsError}}) — using the numbers that are already on file.</div>{{end}}
    {{if .Extensions}}
      <div class="shuttle">
        <div class="pane">
          <div class="pane-header">
            <span class="pane-label">Available</span>
            <span class="pane-count" id="available-count"></span>
          </div>
          <input type="text" placeholder="Search by number or name…" oninput="shuttleFilter('available-select', this.value)">
          <select id="available-select" multiple ondblclick="shuttleMove('available-select','selected-select')">
            {{range .Extensions}}{{if not (index $.ExtListMap .Number)}}
            <option value="{{.Number}}">{{.Number}} — {{.Name}}</option>
            {{end}}{{end}}
          </select>
        </div>
        <div class="shuttle-buttons">
          <button type="button" onclick="shuttleMove('available-select','selected-select')" title="Move selected to filter list">→</button>
          <button type="button" onclick="shuttleMove('selected-select','available-select')" title="Remove selected from filter list">←</button>
          <button type="button" onclick="shuttleMove('available-select','selected-select', true)" title="Add all visible to filter list">⇒</button>
          <button type="button" onclick="shuttleMove('selected-select','available-select', true)" title="Clear filter list">⇐</button>
        </div>
        <div class="pane">
          <div class="pane-header">
            <span class="pane-label">Extensions that are</span>
            <select name="extension_filter_mode" class="inline-mode">
              <option value="exclude" {{if eq .ExtMode "exclude"}}selected{{end}}>Excluded</option>
              <option value="include" {{if eq .ExtMode "include"}}selected{{end}}>Included</option>
              <option value="all"     {{if eq .ExtMode "all"}}selected{{end}}>Ignored (off)</option>
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
      <div class="manual-add">
        <input type="text" id="manual-ext-input" placeholder="e.g. 700" maxlength="10"
               onkeydown="if(event.key==='Enter'){event.preventDefault();addManualExtension();}">
        <button type="button" onclick="addManualExtension()">+ Add number</button>
        <span class="hint">For extensions not in the directory (conferences, IVR…)</span>
      </div>
      <div class="shuttle-dblclick-hint">Double-click a row to move it across.</div>
    {{else}}
      <textarea name="extension_filter" placeholder="908&#10;909&#10;910">{{.ExtList}}</textarea>
      <div class="hint">One extension number per line. Directory lookup from 3CX was not available.</div>
    {{end}}
  </div>

  <div class="actions">
    <button type="submit">Save &amp; apply</button>
    <span class="footer-meta">Config: <code>{{.ConfigPath}}</code></span>
  </div>

</form>
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
