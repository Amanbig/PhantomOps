import {GetSettings, SaveSettings, TestConnection, DetectServices, TestAI, SendTestNotification, GetMonitorStatus} from '../../wailsjs/go/main/App';
import {esc} from '../util.js';

// Describes each connectable service and the form fields it needs.
const TYPES = {
    docker: {
        label: 'Docker / Podman', icon: '🐳',
        blurb: 'Container state, restarts, OOM kills, memory and error logs.',
        fields: [{key: 'socket', label: 'Socket path', placeholder: '/var/run/docker.sock',
            help: 'Leave empty for the default. Podman works through its Docker-compatible socket.'}],
    },
    kubernetes: {
        label: 'Kubernetes', icon: '☸️',
        blurb: 'Pods, events, rollouts and memory (works with k3s). Uses kubectl.',
        fields: [{key: 'context', label: 'kubectl context', placeholder: '(current context)',
            help: 'Leave empty to use the current context.'}],
    },
    prometheus: {
        label: 'Prometheus', icon: '🔥',
        blurb: 'Metric anomalies: memory growth, targets down, firing alerts, 5xx rates.',
        fields: [{key: 'url', label: 'URL', placeholder: 'http://localhost:9090', required: true}],
    },
    loki: {
        label: 'Loki', icon: '📜',
        blurb: 'Error-looking log lines grouped per service.',
        fields: [
            {key: 'url', label: 'URL', placeholder: 'http://localhost:3100', required: true},
            {key: 'query', label: 'LogQL query', placeholder: '(default: error lines from all services)'},
        ],
    },
    nomad: {
        label: 'Nomad', icon: '🧩',
        blurb: 'Job and allocation status, task failures and OOM kills.',
        fields: [
            {key: 'url', label: 'URL', placeholder: 'http://localhost:4646', required: true},
            {key: 'token', label: 'ACL token', type: 'password', placeholder: '(optional)',
                help: 'Stored locally in the config file (owner-only permissions).'},
        ],
    },
    ssh: {
        label: 'SSH host', icon: '🔑',
        blurb: 'Read-only diagnostics: load, memory, disk, failed units, OOM, open ports.',
        fields: [
            {key: 'addr', label: 'Host', placeholder: 'web-01.example.com:22', required: true},
            {key: 'user', label: 'User', placeholder: 'ops', required: true},
            {key: 'keyPath', label: 'Private key path', placeholder: '(empty = use ssh-agent)',
                help: 'Unencrypted key file. Passphrase-protected keys: use ssh-agent.'},
            {key: 'knownHosts', label: 'known_hosts file', placeholder: '~/.ssh/known_hosts'},
            {key: 'expectedPorts', label: 'Expected open ports', placeholder: '80, 443',
                help: 'Comma-separated TCP ports that should be listening.'},
            {key: 'insecure', label: 'Skip host key verification', type: 'checkbox',
                help: 'Insecure. Only for testing.'},
        ],
    },
};

const DEFAULT_COUNT_HINT = 'Add a service to start investigating.';

const AI_PRESETS = {
    ollama: {label: 'Ollama (local)', url: 'http://localhost:11434/v1', keyHelp: 'Not needed for a local Ollama.'},
    openai: {label: 'OpenAI-compatible API', url: 'https://api.openai.com/v1', keyHelp: 'Sent as a Bearer token.'},
};

function isLocalUrl(u) {
    try {
        const h = new URL(u).hostname;
        return h === 'localhost' || h.endsWith('.local') || h === '::1' || h === '[::1]' ||
            /^127\./.test(h) || /^10\./.test(h) || /^192\.168\./.test(h) || /^172\.(1[6-9]|2\d|3[01])\./.test(h);
    } catch { return false; }
}

let state = {settings: null, detected: null, health: {}, form: null, error: '', aiTest: null, aiModels: []};
let root;

export async function mountConnections(el) {
    root = el;
    root.innerHTML = '<p class="muted">Loading…</p>';
    await refresh();
}

async function refresh() {
    try {
        state.settings = await GetSettings();
        state.error = '';
    } catch (err) {
        state.error = String(err);
    }
    render();
}

async function persist(cfg, okMessage) {
    try {
        await SaveSettings(cfg);
        state.error = '';
        await refresh();
        if (okMessage) toast(okMessage);
        return true;
    } catch (err) {
        state.error = String(err);
        render();
        return false;
    }
}

function toast(msg) {
    document.querySelectorAll('.toast').forEach(t => t.remove());
    const t = document.createElement('div');
    t.className = 'toast';
    t.textContent = msg;
    document.body.appendChild(t);
    setTimeout(() => t.remove(), 2500);
}

function cloneConfig() {
    return JSON.parse(JSON.stringify(state.settings.config));
}

function summary(c) {
    switch (c.type) {
        case 'docker': return c.socket || '/var/run/docker.sock';
        case 'kubernetes': return c.context ? `context: ${c.context}` : 'current kubectl context';
        case 'ssh': return `${c.user}@${c.addr}` + (c.expectedPorts?.length ? ` · ports ${c.expectedPorts.join(', ')}` : '');
        default: return c.url;
    }
}

function sameTarget(a, b) {
    return a.type === b.type && (a.socket || '') === (b.socket || '') && (a.context || '') === (b.context || '') &&
        (a.url || '') === (b.url || '') && (a.addr || '') === (b.addr || '');
}

// ---------- rendering ----------

function render() {
    if (!state.settings) {
        root.innerHTML = `<p class="error">${esc(state.error || 'Could not load settings.')}</p>`;
        return;
    }
    const {config, path, saved, notes} = state.settings;
    const conns = config.connections || [];

    root.innerHTML = `
        <div class="toolbar"><h2 class="view-title">Connections</h2>
            <div class="toolbar-actions">
                <button class="btn btn-secondary" id="detect">Detect local services</button>
                <button class="btn" id="add">+ Add connection</button>
            </div></div>
        ${state.error ? `<div class="alert alert-bad">${esc(state.error)}</div>` : ''}
        ${!saved ? `<div class="alert">These connections were auto-detected and are not saved yet.
            <button class="btn btn-small" id="save-defaults">Save them</button></div>` : ''}
        ${(notes || []).map(n => `<div class="alert alert-bad">${esc(n)}</div>`).join('')}
        ${state.detected ? renderDetected(conns) : ''}
        <div class="conn-list">
            ${conns.length ? conns.map(renderCard).join('') : `<div class="empty">
                <div class="empty-icon">🔌</div><h3>No connections yet</h3>
                <p class="muted">${DEFAULT_COUNT_HINT}</p></div>`}
        </div>
        ${renderAI(config.ai || {})}
        ${renderMonitoring(config.monitoring || {})}
        ${renderRemediation(config.remediation || {})}
        ${renderGeneral(config)}
        <p class="muted small">Config file: <code>${esc(path)}</code></p>
        ${state.form ? renderForm() : ''}`;
    bind();
}

function renderCard(c) {
    const t = TYPES[c.type] || {label: c.type, icon: '❔'};
    const h = state.health[c.id];
    const dot = h ? (h.ok ? 'dot-ok' : 'dot-bad') : 'dot-idle';
    return `
        <div class="conn-card ${c.enabled ? '' : 'conn-off'}" data-id="${esc(c.id)}">
            <div class="conn-icon">${t.icon}</div>
            <div class="conn-main">
                <div class="conn-name"><span class="dot ${dot}"></span>${esc(c.name)}
                    <span class="chip">${esc(t.label)}</span></div>
                <div class="conn-sub">${esc(summary(c))}</div>
                ${h ? `<div class="conn-health ${h.ok ? 'ok' : 'bad'}">${esc(h.message)}</div>` : ''}
            </div>
            <div class="conn-actions">
                <label class="switch" title="Enable or disable"><input type="checkbox" data-action="toggle" ${c.enabled ? 'checked' : ''}><span></span></label>
                <button class="btn btn-small btn-secondary" data-action="test">Test</button>
                <button class="btn btn-small btn-secondary" data-action="edit">Edit</button>
                <button class="btn btn-small btn-danger" data-action="delete">Delete</button>
            </div>
        </div>`;
}

function renderDetected(existing) {
    const fresh = state.detected.filter(d => !existing.some(e => sameTarget(e, d)));
    return `<div class="panel">
        <div class="panel-head"><strong>Found on this machine</strong>
            <button class="btn btn-small btn-secondary" id="close-detected">Close</button></div>
        ${fresh.length ? fresh.map((d, i) => {
            const t = TYPES[d.type];
            return `<div class="detected-row"><span>${t.icon} ${esc(d.name)} <span class="muted small">${esc(summary(d))}</span></span>
                <button class="btn btn-small" data-add-detected="${i}">Add</button></div>`;
        }).join('') + (fresh.length > 1 ? '<button class="btn btn-small" id="add-all">Add all</button>' : '')
        : '<p class="muted">Nothing new found. Everything detected is already connected.</p>'}
        </div>`;
}

function renderAI(ai) {
    const provider = ai.provider || 'ollama';
    const url = ai.baseUrl || AI_PRESETS[provider].url;
    const local = isLocalUrl(url);
    return `<div class="panel" id="ai-panel">
        <div class="panel-head"><strong>🤖 AI investigator</strong>
            <label class="check" style="margin:0"><input type="checkbox" id="ai-enabled" ${ai.enabled ? 'checked' : ''}> Enabled</label></div>
        <p class="muted small" style="margin-top:0">A language model double-checks Phantom's rule-based diagnosis. Its answer must cite real timeline events and may only mention services that appear in your data. It can never trigger actions on its own. Off by default.</p>
        <div class="form-grid">
            <label>Provider<select id="ai-provider">${Object.entries(AI_PRESETS).map(([k, p]) =>
                `<option value="${k}" ${k === provider ? 'selected' : ''}>${esc(p.label)}</option>`).join('')}</select></label>
            <label>Model<input id="ai-model" list="ai-models" value="${esc(ai.model || '')}" placeholder="llama3.1:8b">
                <datalist id="ai-models">${state.aiModels.map(m => `<option value="${esc(m)}">`).join('')}</datalist>
                <span class="muted small">Models under ~3B parameters are fast but often give weaker answers than the built-in rules; 7B+ is recommended.</span></label>
            <label class="span2">Base URL<input id="ai-url" value="${esc(url)}" placeholder="${esc(AI_PRESETS[provider].url)}"></label>
            <label class="span2">API key<input id="ai-key" type="password" value="${esc(ai.apiKey || '')}" placeholder="(optional)">
                <span class="muted small">${esc(AI_PRESETS[provider].keyHelp)} Stored locally in the config file (owner-only permissions).</span></label>
        </div>
        <div class="privacy ${local ? 'privacy-local' : 'privacy-remote'}" id="ai-privacy">
            ${local ? '✅ Local endpoint: incident data stays on your machine or network.'
                    : '⚠ Remote endpoint: incident timelines (service names, event details and log excerpts) will be sent to this server.'}</div>
        ${state.aiTest ? `<div class="alert ${state.aiTest.ok ? 'alert-ok' : 'alert-bad'}">${esc(state.aiTest.message)}</div>` : ''}
        <div class="toolbar-actions">
            <button class="btn btn-small btn-secondary" id="ai-test">Test &amp; load models</button>
            <button class="btn btn-small" id="ai-save">Save AI settings</button>
        </div>
    </div>`;
}

function renderMonitoring(m) {
    const sev = m.notifyMinSeverity || 'critical';
    return `<div class="panel" id="mon-panel">
        <div class="panel-head"><strong>⏱ Monitoring</strong>
            <label class="check" style="margin:0"><input type="checkbox" id="mon-enabled" ${m.enabled ? 'checked' : ''}> Scan in the background</label></div>
        <p class="muted small" style="margin:0 0 10px">While PhantomOps is open, it scans on a schedule, keeps the incident history up to date, and can send a desktop notification when something new breaks. Manual scans never notify.</p>
        <div class="form-grid">
            <label>Scan every (minutes)<input id="mon-interval" type="number" min="1" max="1440" value="${esc(m.intervalMinutes || 5)}"></label>
            <label>Notify for<select id="mon-sev">
                <option value="critical" ${sev === 'critical' ? 'selected' : ''}>Critical incidents</option>
                <option value="warning" ${sev === 'warning' ? 'selected' : ''}>Warnings and critical</option></select></label>
        </div>
        <label class="check"><input type="checkbox" id="mon-notify" ${m.notify ? 'checked' : ''}> Desktop notifications (new, escalated, returning and resolved incidents)</label>
        <div id="mon-msg"></div>
        <div class="toolbar-actions">
            <button class="btn btn-small btn-secondary" id="mon-test">Send test notification</button>
            <button class="btn btn-small" id="mon-save">Save monitoring</button>
        </div>
    </div>`;
}

function renderRemediation(rem) {
    return `<div class="panel" id="rem-panel">
        <div class="panel-head"><strong>🛠 Remediation</strong>
            <label class="check" style="margin:0"><input type="checkbox" id="rem-enabled" ${rem.enabled ? 'checked' : ''}> Allow rollbacks</label></div>
        <p class="muted small" style="margin:0 0 8px">When on, an incident that points at a bad Kubernetes or Nomad deployment can be rolled back to its previous revision. Every rollback shows a preview of exactly what changes, needs you to type the workload name, and is recorded on the Activity tab. Never runs automatically. Off by default.</p>
        <button class="btn btn-small" id="rem-save">Save</button>
    </div>`;
}

function renderGeneral(config) {
    return `<div class="panel">
        <div class="panel-head"><strong>Analysis settings</strong></div>
        <div class="form-grid">
            <label>Environment label<input id="g-env" value="${esc(config.environment)}" placeholder="production"></label>
            <label>Look-back window (minutes)<input id="g-window" type="number" min="1" max="1440" value="${esc(config.windowMinutes || 30)}"></label>
            <label class="span2">Ignore subjects (comma-separated substrings)
                <input id="g-ignore" value="${esc((config.ignoreSubjects || []).join(', '))}" placeholder="monitoring, cadvisor"></label>
        </div>
        <button class="btn btn-small" id="save-general">Save settings</button>
    </div>`;
}

function renderForm() {
    const f = state.form;
    if (!f.type) {
        return `<div class="overlay" id="overlay"><div class="modal">
            <div class="modal-head"><h3>Add connection</h3><button class="btn btn-small btn-secondary" data-close>✕</button></div>
            <div class="type-grid">${Object.entries(TYPES).map(([k, t]) => `
                <button class="type-card" data-type="${k}"><div class="type-icon">${t.icon}</div>
                <strong>${esc(t.label)}</strong><span class="muted small">${esc(t.blurb)}</span></button>`).join('')}
            </div></div></div>`;
    }
    const t = TYPES[f.type];
    const c = f.conn;
    return `<div class="overlay" id="overlay"><div class="modal">
        <div class="modal-head"><h3>${t.icon} ${f.editing ? 'Edit' : 'Add'} ${esc(t.label)}</h3>
            <button class="btn btn-small btn-secondary" data-close>✕</button></div>
        <form id="conn-form" autocomplete="off">
            <label>Name<input name="name" required value="${esc(c.name)}"></label>
            ${t.fields.map(fd => {
                const v = fd.key === 'expectedPorts' ? (c.expectedPorts || []).join(', ') : (c[fd.key] ?? '');
                if (fd.type === 'checkbox') {
                    return `<label class="check"><input type="checkbox" name="${fd.key}" ${c[fd.key] ? 'checked' : ''}> ${esc(fd.label)}
                        ${fd.help ? `<span class="muted small">${esc(fd.help)}</span>` : ''}</label>`;
                }
                return `<label>${esc(fd.label)}${fd.required ? ' *' : ''}
                    <input name="${fd.key}" type="${fd.type || 'text'}" value="${esc(v)}" placeholder="${esc(fd.placeholder || '')}" ${fd.required ? 'required' : ''}>
                    ${fd.help ? `<span class="muted small">${esc(fd.help)}</span>` : ''}</label>`;
            }).join('')}
            <label class="check"><input type="checkbox" name="enabled" ${c.enabled ? 'checked' : ''}> Enabled</label>
            <div id="form-test" class="form-test"></div>
            ${f.error ? `<div class="alert alert-bad">${esc(f.error)}</div>` : ''}
            <div class="modal-actions">
                <button type="button" class="btn btn-secondary" id="form-test-btn">Test connection</button>
                <button type="submit" class="btn">${f.editing ? 'Save changes' : 'Add connection'}</button>
            </div>
        </form></div></div>`;
}

// ---------- behaviour ----------

function readForm() {
    const form = root.querySelector('#conn-form');
    const data = new FormData(form);
    const c = {...state.form.conn};
    c.name = (data.get('name') || '').trim();
    c.enabled = form.elements.enabled.checked;
    for (const fd of TYPES[state.form.type].fields) {
        if (fd.type === 'checkbox') { c[fd.key] = form.elements[fd.key].checked; continue; }
        const raw = (data.get(fd.key) || '').toString().trim();
        if (fd.key === 'expectedPorts') {
            c.expectedPorts = raw ? raw.split(',').map(s => parseInt(s.trim(), 10)) : [];
        } else {
            c[fd.key] = raw;
        }
    }
    return c;
}

function blankConn(type) {
    const t = TYPES[type];
    return {id: '', type, name: t.label, enabled: true, socket: '', context: '', url: '', query: '', token: '',
        addr: '', user: '', keyPath: '', knownHosts: '', insecure: false, expectedPorts: []};
}

function bind() {
    const $ = s => root.querySelector(s);

    $('#add')?.addEventListener('click', () => { state.form = {type: '', conn: null}; render(); });
    $('#detect')?.addEventListener('click', async e => {
        e.target.disabled = true; e.target.textContent = 'Detecting…';
        try { state.detected = await DetectServices(); } catch (err) { state.error = String(err); }
        render();
    });
    $('#close-detected')?.addEventListener('click', () => { state.detected = null; render(); });
    $('#save-defaults')?.addEventListener('click', () => persist(cloneConfig(), 'Connections saved'));

    root.querySelectorAll('[data-add-detected]').forEach(b => b.addEventListener('click', () => {
        const fresh = state.detected.filter(d => !state.settings.config.connections.some(e => sameTarget(e, d)));
        const cfg = cloneConfig();
        cfg.connections.push(fresh[Number(b.dataset.addDetected)]);
        persist(cfg, 'Connection added');
    }));
    $('#add-all')?.addEventListener('click', () => {
        const cfg = cloneConfig();
        cfg.connections.push(...state.detected.filter(d => !cfg.connections.some(e => sameTarget(e, d))));
        persist(cfg, 'Connections added');
    });

    root.querySelectorAll('.conn-card').forEach(card => {
        const id = card.dataset.id;
        const find = () => state.settings.config.connections.find(c => c.id === id);
        card.querySelector('[data-action=toggle]').addEventListener('change', e => {
            const cfg = cloneConfig();
            cfg.connections.find(c => c.id === id).enabled = e.target.checked;
            persist(cfg);
        });
        card.querySelector('[data-action=test]').addEventListener('click', async e => {
            e.target.disabled = true; e.target.textContent = 'Testing…';
            state.health[id] = await TestConnection(find());
            render();
        });
        card.querySelector('[data-action=edit]').addEventListener('click', () => {
            const c = find();
            state.form = {type: c.type, conn: {...c}, editing: true, error: ''};
            render();
        });
        card.querySelector('[data-action=delete]').addEventListener('click', () => {
            const c = find();
            if (!confirm(`Delete connection "${c.name}"?`)) return;
            const cfg = cloneConfig();
            cfg.connections = cfg.connections.filter(x => x.id !== id);
            delete state.health[id];
            persist(cfg, 'Connection deleted');
        });
    });

    // AI panel
    const readAI = () => ({
        enabled: $('#ai-enabled').checked, provider: $('#ai-provider').value, baseUrl: $('#ai-url').value.trim(),
        model: $('#ai-model').value.trim(), apiKey: $('#ai-key').value,
    });
    $('#ai-provider')?.addEventListener('change', e => {
        const urlEl = $('#ai-url');
        const known = Object.values(AI_PRESETS).map(p => p.url);
        if (!urlEl.value.trim() || known.includes(urlEl.value.trim())) urlEl.value = AI_PRESETS[e.target.value].url;
        state.aiTest = null;
        const cfg = cloneConfig(); cfg.ai = {...readAI()}; state.settings.config = cfg;
        render();
    });
    $('#ai-url')?.addEventListener('input', e => {
        const local = isLocalUrl(e.target.value);
        const el = $('#ai-privacy');
        el.className = 'privacy ' + (local ? 'privacy-local' : 'privacy-remote');
        el.textContent = local ? '✅ Local endpoint: incident data stays on your machine or network.'
            : '⚠ Remote endpoint: incident timelines (service names, event details and log excerpts) will be sent to this server.';
    });
    $('#ai-test')?.addEventListener('click', async e => {
        e.target.disabled = true; e.target.textContent = 'Testing…';
        const res = await TestAI(readAI());
        state.aiTest = res;
        state.aiModels = res.models || [];
        const cfg = cloneConfig(); cfg.ai = {...readAI()}; state.settings.config = cfg;
        render();
    });
    $('#ai-save')?.addEventListener('click', async () => {
        const ai = readAI();
        if (ai.enabled && !isLocalUrl(ai.baseUrl) &&
            !confirm(`Incident data (service names, event details, log excerpts) will be sent to ${new URL(ai.baseUrl).host}. Continue?`)) return;
        const cfg = cloneConfig();
        cfg.ai = ai;
        state.aiTest = null;
        persist(cfg, 'AI settings saved');
    });

    $('#mon-test')?.addEventListener('click', async () => {
        const msg = $('#mon-msg');
        try { await SendTestNotification(); msg.innerHTML = '<div class="alert alert-ok">Test notification sent. If nothing appeared, check that your desktop allows notifications.</div>'; }
        catch (err) { msg.innerHTML = `<div class="alert alert-bad">${esc(String(err))}</div>`; }
    });
    $('#mon-save')?.addEventListener('click', () => {
        const cfg = cloneConfig();
        cfg.monitoring = {
            enabled: $('#mon-enabled').checked,
            intervalMinutes: parseInt($('#mon-interval').value, 10) || 5,
            notify: $('#mon-notify').checked,
            notifyMinSeverity: $('#mon-sev').value,
        };
        persist(cfg, cfg.monitoring.enabled ? `Monitoring every ${cfg.monitoring.intervalMinutes} min` : 'Monitoring off');
    });

    $('#rem-save')?.addEventListener('click', () => {
        const enabled = $('#rem-enabled').checked;
        if (enabled && !confirm('Allow PhantomOps to roll back Kubernetes and Nomad deployments when you approve it? Each rollback still needs your explicit confirmation.')) return;
        const cfg = cloneConfig();
        cfg.remediation = {enabled};
        persist(cfg, enabled ? 'Rollbacks allowed (with confirmation)' : 'Rollbacks disabled');
    });

    $('#save-general')?.addEventListener('click', () => {
        const cfg = cloneConfig();
        cfg.environment = $('#g-env').value.trim();
        cfg.windowMinutes = parseInt($('#g-window').value, 10) || 30;
        cfg.ignoreSubjects = $('#g-ignore').value.split(',').map(s => s.trim()).filter(Boolean);
        persist(cfg, 'Settings saved');
    });

    // modal
    root.querySelectorAll('[data-close]').forEach(b => b.addEventListener('click', closeForm));
    $('#overlay')?.addEventListener('mousedown', e => { if (e.target.id === 'overlay') closeForm(); });
    root.querySelectorAll('[data-type]').forEach(b => b.addEventListener('click', () => {
        state.form = {type: b.dataset.type, conn: blankConn(b.dataset.type), editing: false, error: ''};
        render();
    }));
    $('#form-test-btn')?.addEventListener('click', async e => {
        const out = $('#form-test');
        e.target.disabled = true; e.target.textContent = 'Testing…';
        out.innerHTML = '';
        const res = await TestConnection(readForm());
        out.innerHTML = `<div class="alert ${res.ok ? 'alert-ok' : 'alert-bad'}">${esc(res.message)}</div>`;
        e.target.disabled = false; e.target.textContent = 'Test connection';
    });
    $('#conn-form')?.addEventListener('submit', async e => {
        e.preventDefault();
        const c = readForm();
        const cfg = cloneConfig();
        if (state.form.editing) {
            cfg.connections = cfg.connections.map(x => x.id === c.id ? c : x);
        } else {
            cfg.connections.push(c);
        }
        const wasEditing = state.form.editing;
        const ok = await persist(cfg, wasEditing ? 'Connection updated' : 'Connection added');
        if (ok) state.form = null;
        else if (state.form) { state.form.error = state.error; state.error = ''; }
        render();
    });
}

function closeForm() { state.form = null; render(); }
