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

// Theme toggle
function toggleTheme() {
	var el = document.documentElement;
	var cur = el.getAttribute('data-theme');
	var next = cur === 'dark' ? 'light' : 'dark';
	el.setAttribute('data-theme', next);
	localStorage.setItem('angrybox-theme', next);
	updateThemeIcons(next);
}
function updateThemeIcons(theme) {
	var sun = document.getElementById('ico-sun');
	var moon = document.getElementById('ico-moon');
	if (sun) sun.classList.toggle('hidden', theme === 'dark');
	if (moon) moon.classList.toggle('hidden', theme === 'light');
}
(function(){
	var t = localStorage.getItem('angrybox-theme') || 'dark';
	updateThemeIcons(t);
})();

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
