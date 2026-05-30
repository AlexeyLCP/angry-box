// Angry-BOX UI helpers
function addSSHKey() {
    var d = document.createElement('div');
    d.className = 'flex gap-2 items-end';
    d.innerHTML = '<div class="form-control flex-1"><input type="text" name="ssh_key_name" class="input input-bordered input-sm" placeholder="Key name" /></div><div class="form-control flex-1"><input type="text" name="ssh_key_path" class="input input-bordered input-sm" placeholder="/path/to/key" /></div><button type="button" class="btn btn-ghost btn-xs text-error" onclick="this.parentElement.remove()">✕</button>';
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
        if (typeof htmx !== 'undefined') {
            htmx.process(clone);
        }
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

// HTMX loading bar
var loadingBar = document.getElementById('htmx-loading-bar');
if (loadingBar) {
    document.body.addEventListener('htmx:beforeRequest', function() { loadingBar.classList.add('active'); });
    document.body.addEventListener('htmx:afterRequest', function() { loadingBar.classList.remove('active'); });
}
