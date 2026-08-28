// Angry-BOX UI helpers
// AB_I18N is server-rendered in base.templ; t(key) returns the active-language string.
function abt(k){ return (window.AB_I18N && window.AB_I18N.t) ? window.AB_I18N.t(k) : k; }
function addSSHKey() {
    var d = document.createElement('div');
    d.className = 'flex gap-2 items-end';
    d.innerHTML = '<div class="form-control flex-1"><input type="text" name="ssh_key_name" class="input input-bordered input-sm" placeholder="'+abt('Key name')+'" /></div><div class="form-control flex-1"><input type="text" name="ssh_key_path" class="input input-bordered input-sm" placeholder="'+abt('/path/to/key')+'" /></div><button type="button" class="btn btn-ghost btn-xs text-error" onclick="this.parentElement.remove()">✕</button>';
    document.getElementById('ssh-keys-list').appendChild(d);
}

function addInboundRow() {
    var tmpl = document.getElementById('inbound-tmpl');
    if (!tmpl) return;
    var clone = tmpl.content.firstElementChild.cloneNode(true);
    var idx = Date.now().toString(); // unique ID

    // Add hidden index field
    var hidden = document.createElement('input');
    hidden.type = 'hidden';
    hidden.name = 'inbound_index';
    hidden.value = idx;
    clone.appendChild(hidden);

    // Rename for_users checkboxes
    var checkboxes = clone.querySelectorAll('input[name="for_users"]');
    checkboxes.forEach(function(cb) {
        cb.name = 'for_users_' + idx;
    });

    var list = document.getElementById('inbounds-list');
    if (list) {
        list.appendChild(clone);
        // Apply initial preset filtering based on default protocol (awg)
        var protoSelect = clone.querySelector('select[name="proto"]');
        if (protoSelect) {
            filterPresetsForRow(protoSelect);
        }
        if (typeof htmx !== 'undefined') {
            htmx.process(clone);
        }
    }
}

// Filter preset dropdown options based on selected protocol.
function filterPresetsForRow(protoSelect) {
    var row = protoSelect.closest('.inbound-row');
    if (!row) return;
    var presetSelect = row.querySelector('select[name="obfuscation"]');
    if (!presetSelect) return;

    var protocol = protoSelect.value;

    // Read presets data from hidden span inside dialog
    var allowed = [];
    var dataEl = document.getElementById('protocol-presets-data');
    if (dataEl && dataEl.textContent) {
        try {
            var presetsMap = JSON.parse(dataEl.textContent);
            if (presetsMap && presetsMap[protocol]) {
                allowed = presetsMap[protocol];
            }
        } catch (e) {
            // If JSON parse fails, leave allowed empty (shows only "None")
        }
    }

    var currentValue = presetSelect.value;
    // Clear existing options safely
    while (presetSelect.firstChild) {
        presetSelect.removeChild(presetSelect.firstChild);
    }

    // Always add "None" option
    var noneOpt = document.createElement('option');
    noneOpt.value = '';
    noneOpt.textContent = abt('None');
    presetSelect.appendChild(noneOpt);

    var found = false;
    for (var i = 0; i < allowed.length; i++) {
        var opt = document.createElement('option');
        opt.value = allowed[i];
        opt.textContent = allowed[i];
        if (allowed[i] === currentValue) found = true;
        presetSelect.appendChild(opt);
    }

    // Restore previous selection if still valid
    if (found) {
        presetSelect.value = currentValue;
    }
}

// Page title + sidebar highlight
(function() {
    function updateUI() {
        var main = document.getElementById('main-content');
        if (main) {
            var h2 = main.querySelector('h2');
            if (h2 && h2.textContent) {
                document.title = h2.textContent.trim() + ' | Angry-BOX';
                var pt = document.getElementById('page-title');
                if (pt) pt.textContent = h2.textContent.trim();
            }
        }
        var path = window.location.pathname;
        document.querySelectorAll('.menu a').forEach(function(link) {
            link.classList.remove('sidebar-active');
            if (link.getAttribute('hx-get') === path) link.classList.add('sidebar-active');
        });
    }
    document.body.addEventListener('htmx:afterSettle', updateUI);
    updateUI();
})();

// Theme system — the Angry-BOX / Lovable palette: 4 selectable themes
// (sand/slate/graphite/night) keyed by [data-theme="..."] in
// /static/css/themes.css. Persisted in localStorage('angrybox-theme');
// default 'sand' (light, warm beige — the current brand).
var AB_THEMES = ['sand', 'slate', 'graphite', 'night'];
var AB_DARK = 'graphite';   // canonical dark theme
var AB_LIGHT = 'sand';      // canonical light theme (also the default)
// Legacy Tokyo Night → new-theme migration map (run once per stored value).
var AB_LEGACY = {
	'tokyonight': 'graphite', 'tokyonight-storm': 'graphite',
	'tokyonight-day': 'sand', 'dark': 'graphite', 'light': 'sand'
};

function setTheme(name) {
	if (AB_THEMES.indexOf(name) < 0) name = AB_LIGHT;
	document.documentElement.setAttribute('data-theme', name);
	localStorage.setItem('angrybox-theme', name);
	updateThemeIcons(name);
	// keep <meta name="theme-color"> in sync for mobile browser chrome
	var meta = document.querySelector('meta[name="theme-color"]');
	if (meta) {
		var colors = {'sand':'#f6f2ea','slate':'#eef0f3','graphite':'#17181a','night':'#0e0e10'};
		meta.setAttribute('content', colors[name] || '#f6f2ea');
	}
}
function toggleTheme() {
	// dark/light shortcut: flip between the canonical dark and light themes.
	var cur = document.documentElement.getAttribute('data-theme');
	var next = (cur === AB_LIGHT) ? AB_DARK : AB_LIGHT;
	setTheme(next);
}
function updateThemeIcons(theme) {
	var sun = document.getElementById('ico-sun');
	var moon = document.getElementById('ico-moon');
	// Show sun when in a LIGHT theme (click → go dark), moon when in a DARK theme.
	var isLight = (theme === AB_LIGHT || theme === 'slate');
	if (sun) sun.classList.toggle('hidden', !isLight);
	if (moon) moon.classList.toggle('hidden', isLight);
}
(function(){
	// Migrate legacy Tokyo Night / 'dark'/'light' stored values so an existing
	// user keeps a sensible theme (old dark → graphite, light → sand).
	var t = localStorage.getItem('angrybox-theme') || AB_LIGHT;
	if (AB_LEGACY[t]) t = AB_LEGACY[t];
	if (AB_THEMES.indexOf(t) < 0) t = AB_LIGHT;
	document.documentElement.setAttribute('data-theme', t);
	updateThemeIcons(t);
})();

// Wire the theme dropdown options (the buttons carry data-set-theme).
document.addEventListener('DOMContentLoaded', function() {
	document.querySelectorAll('[data-set-theme]').forEach(function(btn) {
		btn.addEventListener('click', function() {
			setTheme(btn.getAttribute('data-set-theme'));
			// close the dropdown (DaisyUI dropdown closes on blur; force it)
			if (document.activeElement && document.activeElement.blur) document.activeElement.blur();
		});
	});
});

// HTMX loading bar
var loadingBar = document.getElementById('htmx-loading-bar');
if (loadingBar) {
    document.body.addEventListener('htmx:beforeRequest', function() { loadingBar.classList.add('active'); });
    document.body.addEventListener('htmx:afterRequest', function() { loadingBar.classList.remove('active'); });
}

// Initialize preset filtering when inbounds modal is loaded via HTMX
document.body.addEventListener('htmx:afterSettle', function() {
    var modal = document.getElementById('inbounds-modal');
    if (modal && modal.open) {
        document.querySelectorAll('#inbounds-list select[name="proto"]').forEach(function(sel) {
            filterPresetsForRow(sel);
        });
    }
});

// Show the AWG CPS section only when user_protocol == "awg", and initialize
// the capture-domain wrap visibility based on the mimicry select's value.
// The capture-domain wrap is always in the DOM (so hx-include="closest form"
// always sends the input); we only toggle its display here.
function toggleAWGCPSSection() {
    document.querySelectorAll('select[name="user_protocol"]').forEach(function(sel) {
        var form = sel.closest('form');
        if (!form) return;
        var section = form.querySelector('#awg-cps-section');
        if (section) {
            section.style.display = (sel.value === 'awg') ? 'block' : 'none';
            if (sel.value === 'awg') {
                var mimicry = section.querySelector('select[name="awg_cps_mimicry"]');
                var wrap = section.querySelector('.capture-domain-wrap');
                if (wrap && mimicry) {
                    wrap.style.display = (mimicry.value === 'quic-live') ? 'block' : 'none';
                }
            }
        }
    });
}
document.addEventListener('DOMContentLoaded', toggleAWGCPSSection);
document.addEventListener('htmx:afterSettle', toggleAWGCPSSection);


// initPresetFormSections reveals the per-protocol field section matching the
// selected protocol in the preset create/edit modal (#preset-modal). Runs on
// initial DOMContentLoaded and after each htmx:afterSettle so an EDIT of an
// existing preset shows the right section on load.
function initPresetFormSections() {
	var modal = document.getElementById('preset-modal');
	if (!modal) return;
	var sel = modal.querySelector('select[name="protocol"]');
	if (!sel) return;
	var map = {awg:'.awg-fields','vless-reality':'.reality-fields',xhttp:'.xhttp-fields'};
	['.awg-fields','.reality-fields','.xhttp-fields'].forEach(function(c){
		var el = modal.querySelector(c);
		if (el) el.style.display = 'none';
	});
	var m = map[sel.value];
	if (m) { var el = modal.querySelector(m); if (el) el.style.display = 'block'; }
}
document.addEventListener('DOMContentLoaded', initPresetFormSections);
document.addEventListener('htmx:afterSettle', initPresetFormSections);

// ── P0b Slice 1: user wizard step toggle + sub URL copy ───────────────────────
// wizardNext/wizardPrev toggle fieldset.wizard-step visibility + the DaisyUI
// steps indicator. Single form, one POST — no server-side wizard state
// (AGENTS.md #1 minimal-JS). Also rebinds the Back/Next button labels to the
// step titles so the operator knows which way they're moving.
function wizardCurrentStep(form) {
    var steps = form.querySelectorAll('fieldset.wizard-step');
    for (var i = 0; i < steps.length; i++) {
        if (steps[i].style.display !== 'none') return i + 1;
    }
    return 1;
}
function wizardShowStep(form, n) {
    var steps = form.querySelectorAll('fieldset.wizard-step');
    var max = steps.length;
    if (n < 1) n = 1;
    if (n > max) n = max;
    for (var i = 0; i < steps.length; i++) {
        steps[i].style.display = (i + 1 === n) ? 'block' : 'none';
    }
    // Update the steps indicator (mark steps < n as primary/completed).
    var lis = form.querySelectorAll('ul.steps li');
    for (var j = 0; j < lis.length; j++) {
        if ((j + 1) <= n) {
            lis[j].classList.add('step-primary');
        } else {
            lis[j].classList.remove('step-primary');
        }
    }
    // Update the nav button labels.
    var back = form.querySelector('button[onclick="wizardPrev()"]');
    var next = form.querySelector('button[onclick="wizardNext()"]');
    if (back) back.style.visibility = (n <= 1) ? 'hidden' : 'visible';
    if (next) {
        if (n >= max) {
            next.style.display = 'none';
        } else {
            next.style.display = '';
            next.textContent = lis[n] ? lis[n].textContent.trim() + ' →' : '→';
        }
    }
}
function wizardNext() { wizardShowStep(document.querySelector('dialog#user-modal form'), wizardCurrentStep(document.querySelector('dialog#user-modal form')) + 1); }
function wizardPrev() { wizardShowStep(document.querySelector('dialog#user-modal form'), wizardCurrentStep(document.querySelector('dialog#user-modal form')) - 1); }

function copyText(text, btn) {
    function done() {
        if (!btn) return;
        var orig = btn.textContent;
        btn.textContent = btn.getAttribute('data-copied') || 'Copied';
        setTimeout(function(){ btn.textContent = orig; }, 1500);
    }
    function fallback() {
        var ta = document.createElement('textarea');
        ta.value = text;
        ta.style.position = 'fixed';
        ta.style.left = '-9999px';
        document.body.appendChild(ta);
        ta.select();
        try { document.execCommand('copy'); } catch (e) {}
        document.body.removeChild(ta);
        done();
    }
    if (navigator.clipboard && window.isSecureContext) {
        navigator.clipboard.writeText(text).then(done).catch(fallback);
    } else {
        fallback();
    }
}

function copySubURL(btn) {
    var el = document.getElementById(btn.getAttribute('data-target'));
    if (!el) return;
    el.select();
    copyText(el.value, btn);
}

function copyUserSub(btn) {
    copyText(location.origin + '/sub/' + btn.getAttribute('data-token'), btn);
}

function copyUserConfig(btn) {
    var block = btn.closest('.tn-card');
    if (!block) return;
    var ta = block.querySelector('textarea');
    if (!ta) return;
    copyText(ta.value, btn);
}

function rrMatchChanged(sel) {
    var f = sel.closest('form');
    if (!f) return;
    var preset = sel.value === 'preset';
    var geo = sel.value === 'geosite' || sel.value === 'geoip';
    var p = f.querySelector('.rr-preset');
    var v = f.querySelector('.rr-values');
    var g = f.querySelector('.rr-geo');
    if (p) p.style.display = preset ? 'block' : 'none';
    if (v) v.style.display = preset ? 'none' : 'block';
    if (g) g.style.display = geo ? 'flex' : 'none';
    var chips = f.querySelectorAll('[data-geo-kind]');
    for (var i = 0; i < chips.length; i++) {
        chips[i].style.display = chips[i].getAttribute('data-geo-kind') === sel.value ? '' : 'none';
    }
}

function rrActionChanged(sel) {
    var o = sel.closest('form').querySelector('.rr-outbound');
    if (o) o.style.display = sel.value === 'route' ? 'block' : 'none';
}

function ibProtoChanged(sel) {
    var f = sel.closest('form');
    if (!f) return;
    var v = sel.value;
    function show(cls, on) {
        var el = f.querySelector(cls);
        if (el) el.style.display = on ? 'block' : 'none';
    }
    show('.awg-capture-section', v === 'awg');
    show('.mieru-section', v === 'mieru');
    show('.dest-section', v === 'vless-reality' || v === 'mtproxy');
    show('.vless-section', v === 'vless-reality');
    show('.tls-need-section', v === 'naive' || v === 'trusttunnel');
    show('.mtproxy-section', v === 'mtproxy');
}

function rrChip(btn) {
    var ta = btn.closest('form').querySelector('#rr-values');
    if (!ta) return;
    var val = btn.getAttribute('data-val');
    ta.value = ta.value.trim() ? ta.value.replace(/[,\s]+$/, '') + ', ' + val : val;
}

// addNodeOpenCapture generates a fresh node id and opens the capture wizard in
// the #modal-container via htmx.ajax — same target/swapping as the row "Capture"
// button. We MUST NOT do a full-page navigation here: handleNodeCaptureForm
// returns a raw NodeCaptureForm (no Base layout / no Tailwind / no htmx script),
// so a full-page GET would render a bare <dialog> with no styles and a form
// whose hx-post never fires (htmx.js isn't loaded) — the symptom is "the page
// refreshes and nothing happens, no error" (reported v0.8.3).
function addNodeOpenCapture() {
    var id = 'n' + Math.floor(Date.now()/1000) + Math.floor(Math.random()*100);
    var target = document.getElementById('modal-container');
    if (!target || typeof htmx === 'undefined') { return; }
    htmx.ajax('GET', '/ui/nodes/' + id + '/capture', { target: '#modal-container', swap: 'innerHTML' });
}

// downloadUserConfig reads the config <textarea> from the button's config block
// and saves it as a file via a client-side Blob (no backend round-trip). The
// filename comes from data-filename ("<userName>-<chain>.conf"); AWG configs
// (multi-line awg-quick .conf containing [Interface]) keep .conf, single-line
// URI/share links get .txt so the OS doesn't mis-handle them. Sanitized in JS
// so user/chain names with spaces or slashes don't break the download.
function downloadUserConfig(btn) {
    var block = btn.closest('.tn-card');
    if (!block) { return; }
    var ta = block.querySelector('textarea');
    if (!ta) { return; }
    var content = ta.value;
    var rawName = btn.getAttribute('data-filename') || 'config.conf';
    // Strip any path separators / control chars from the filename.
    var name = rawName.replace(/[\\\/]+/g, '-').replace(/[^\w.\-]+/g, '_');
    // AWG awg-quick .conf is multi-line and starts with [Interface]; a share URI
    // (vless://, tg://, https://...) is single-line — pick the extension so the
    // file opens with the right app.
    if (content.indexOf('[Interface]') === -1 && content.indexOf('\n') === -1) {
        name = name.replace(/\.conf$/i, '') + '.txt';
    }
    var blob = new Blob([content], { type: 'text/plain;charset=utf-8' });
    var url = URL.createObjectURL(blob);
    var a = document.createElement('a');
    a.href = url;
    a.download = name;
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
    setTimeout(function(){ URL.revokeObjectURL(url); }, 1000);
}
