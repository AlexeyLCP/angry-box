// spider.js — native SVG pan/zoom + node drag with persistent positions for the
// Spider Web topology editor. No external libraries.
//
// State: the <g id="spider-viewport"> carries transform="translate(tx,ty) scale(s)".
// Pan: drag on the background rect updates tx/ty. Zoom: wheel updates scale (and
// adjusts translate so the point under the cursor stays put). Node drag: drag
// a .spider-node updates its circle/line coords in SVG-user-space and, on
// mouseup, POSTs the new position to /ui/spider/nodes/{id}/position.

(function () {
  var svg, viewport, catcher;
  var view = { tx: 0, ty: 0, scale: 1 };
  var MIN_SCALE = 0.3, MAX_SCALE = 3;

  function setTransform() {
    if (!viewport) return;
    viewport.setAttribute('transform',
      'translate(' + view.tx + ',' + view.ty + ') scale(' + view.scale + ')');
  }

  // Convert a client (screen) point to SVG user-space coordinates (undo the
  // viewport transform). Used during node drag so the dragged position is in
  // the same coordinate space as the node attributes.
  function clientToSVGPoint(clientX, clientY) {
    var pt = svg.createSVGPoint();
    pt.x = clientX; pt.y = clientY;
    var ctm = viewport.getScreenCTM();
    if (!ctm) return { x: clientX, y: clientY };
    var inv = ctm.inverse();
    return pt.matrixTransform(inv);
  }

  // ─── Pan (drag background) ────────────────────────────────────────────────
  function initPan() {
    var panning = false, startClientX = 0, startClientY = 0, startTX = 0, startTY = 0;
    catcher.addEventListener('mousedown', function (e) {
      if (e.target.closest('.spider-node')) return; // node drag, not pan
      panning = true;
      startClientX = e.clientX; startClientY = e.clientY;
      startTX = view.tx; startTY = view.ty;
      svg.style.cursor = 'grabbing';
      e.preventDefault();
    });
    window.addEventListener('mousemove', function (e) {
      if (!panning) return;
      view.tx = startTX + (e.clientX - startClientX);
      view.ty = startTY + (e.clientY - startClientY);
      setTransform();
    });
    window.addEventListener('mouseup', function () {
      if (panning) { panning = false; svg.style.cursor = 'grab'; }
    });
  }

  // ─── Zoom (wheel) ───────────────────────────────────────────────────────────
  function initZoom() {
    svg.addEventListener('wheel', function (e) {
      e.preventDefault();
      var factor = e.deltaY < 0 ? 1.1 : 1 / 1.1;
      var newScale = Math.max(MIN_SCALE, Math.min(MAX_SCALE, view.scale * factor));
      if (newScale === view.scale) return;
      // Keep the point under the cursor stationary: convert cursor to SVG coords
      // before and after the scale change, and adjust translate by the delta.
      var pt = svg.createSVGPoint();
      pt.x = e.clientX; pt.y = e.clientY;
      var svgPt = pt.matrixTransform(svg.getScreenCTM().inverse());
      // Cursor in current viewport space: (svgPt - t) / scale
      var vx = (svgPt.x - view.tx) / view.scale;
      var vy = (svgPt.y - view.ty) / view.scale;
      view.scale = newScale;
      view.tx = svgPt.x - vx * newScale;
      view.ty = svgPt.y - vy * newScale;
      setTransform();
    }, { passive: false });
  }

  // ─── Node drag with persist ─────────────────────────────────────────────────
  function initNodeDrag() {
    var dragged = null, offsetX = 0, offsetY = 0;
    svg.addEventListener('mousedown', function (e) {
      var node = e.target.closest('.spider-node');
      if (!node) return;
      dragged = node;
      var pt = clientToSVGPoint(e.clientX, e.clientY);
      var inner = node.querySelector('.node-inner');
      var cx = parseFloat(inner.getAttribute('cx'));
      var cy = parseFloat(inner.getAttribute('cy'));
      offsetX = cx - pt.x;
      offsetY = cy - pt.y;
      node.style.cursor = 'grabbing';
      e.preventDefault();
      e.stopPropagation(); // don't start a pan
    });
    window.addEventListener('mousemove', function (e) {
      if (!dragged) return;
      var pt = clientToSVGPoint(e.clientX, e.clientY);
      var nx = pt.x + offsetX;
      var ny = pt.y + offsetY;
      moveNode(dragged, nx, ny);
    });
    window.addEventListener('mouseup', function (e) {
      if (!dragged) return;
      var node = dragged;
      dragged = null;
      node.style.cursor = 'move';
      // Persist the new position.
      var inner = node.querySelector('.node-inner');
      var x = parseFloat(inner.getAttribute('cx'));
      var y = parseFloat(inner.getAttribute('cy'));
      var id = node.getAttribute('data-node-id');
      // Fire-and-forget POST; debounced implicitly (one POST per mouseup).
      fetch('/ui/spider/nodes/' + encodeURIComponent(id) + '/position', {
        method: 'POST',
        headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
        body: 'x=' + encodeURIComponent(x) + '&y=' + encodeURIComponent(y),
        credentials: 'include'
      });
    });
  }

  // moveNode updates all the circles/texts of a node group AND any connected
  // link endpoints (lines with data-from/data-to matching the node id).
  function moveNode(node, nx, ny) {
    var circles = node.querySelectorAll('circle');
    circles[0].setAttribute('cx', nx); circles[0].setAttribute('cy', ny);
    circles[1].setAttribute('cx', nx); circles[1].setAttribute('cy', ny);
    circles[2].setAttribute('cx', nx + 10); circles[2].setAttribute('cy', ny - 10);
    var texts = node.querySelectorAll('text');
    if (texts[0]) { texts[0].setAttribute('x', nx); texts[0].setAttribute('y', ny - 18); }
    if (texts[1]) { texts[1].setAttribute('x', nx); texts[1].setAttribute('y', ny + 4); }
    if (texts[2]) { texts[2].setAttribute('x', nx); texts[2].setAttribute('y', ny + 14); }

    var nodeId = node.getAttribute('data-node-id');
    var lines = svg.querySelectorAll('line[data-from], line[data-to]');
    lines.forEach(function (line) {
      var tx, label;
      if (line.getAttribute('data-from') === nodeId) {
        line.setAttribute('x1', nx); line.setAttribute('y1', ny);
        tx = line.nextElementSibling;
        if (tx && tx.tagName === 'text') {
          tx.setAttribute('x', (nx + parseFloat(line.getAttribute('x2'))) / 2);
          tx.setAttribute('y', (ny + parseFloat(line.getAttribute('y2'))) / 2 - 8);
        }
      }
      if (line.getAttribute('data-to') === nodeId) {
        line.setAttribute('x2', nx); line.setAttribute('y2', ny);
        tx = line.nextElementSibling;
        if (tx && tx.tagName === 'text') {
          tx.setAttribute('x', (parseFloat(line.getAttribute('x1')) + nx) / 2);
          tx.setAttribute('y', (parseFloat(line.getAttribute('y1')) + ny) / 2 - 8);
        }
      }
    });
  }

  // ─── Public controls ───────────────────────────────────────────────────────
  window.spiderZoom = function (factor) {
    var newScale = Math.max(MIN_SCALE, Math.min(MAX_SCALE, view.scale * factor));
    if (newScale === view.scale) return;
    // Zoom around the SVG center.
    var cx = 800, cy = 450;
    var vx = (cx - view.tx) / view.scale;
    var vy = (cy - view.ty) / view.scale;
    view.scale = newScale;
    view.tx = cx - vx * newScale;
    view.ty = cy - vy * newScale;
    setTransform();
  };
  window.spiderReset = function () {
    view = { tx: 0, ty: 0, scale: 1 };
    setTransform();
  };

  // ─── Init ───────────────────────────────────────────────────────────────────
  window.initSpiderInteraction = function () {
    svg = document.getElementById('spider-svg');
    if (!svg) return;
    viewport = document.getElementById('spider-viewport');
    catcher = document.getElementById('spider-pan-catcher');
    if (!viewport || !catcher) return;
    setTransform();
    initPan();
    initZoom();
    initNodeDrag();
  };
})();