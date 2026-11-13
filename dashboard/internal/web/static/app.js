// raspberry tunnel: the page's only script, hand-written, no build step (D3).
// It does three things, and the page works without it:
//   1. the colour scheme switch: the system's until the viewer picks one,
//      then theirs, remembered in this browser;
//   2. the shared hover marker across the charts, from data the server put
//      in the page;
//   3. swapping in fresh server-rendered HTML every 30 seconds.
// It loads in <head>, so the picked scheme applies before the first paint.
(function () {
  'use strict';

  var KEY = 'rt-theme';
  var root = document.documentElement;

  function stored() {
    try { return localStorage.getItem(KEY); } catch (e) { return null; }
  }
  function store(t) {
    try {
      if (t === 'system') localStorage.removeItem(KEY); else localStorage.setItem(KEY, t);
    } catch (e) { /* private mode: the choice lasts until reload */ }
  }
  function apply(t) {
    root.classList.remove('light', 'dark');
    if (t === 'light' || t === 'dark') root.classList.add(t);
  }
  apply(stored());

  function make(tag, cls, text) {
    var el = document.createElement(tag);
    if (cls) el.className = cls;
    if (text != null) el.textContent = text;
    return el;
  }

  function start() {
    // 1. The colour scheme switch, hidden until there's a script to run it.
    var group = document.getElementById('rt-theme');
    if (group) {
      var buttons = group.querySelectorAll('button[data-theme]');
      var mark = function (t) {
        for (var i = 0; i < buttons.length; i++) {
          buttons[i].setAttribute('aria-pressed', String(buttons[i].getAttribute('data-theme') === t));
        }
      };
      var cur = stored();
      mark(cur === 'light' || cur === 'dark' ? cur : 'system');
      group.hidden = false;
      group.addEventListener('click', function (e) {
        var b = e.target.closest('button[data-theme]');
        if (!b) return;
        var t = b.getAttribute('data-theme');
        store(t);
        apply(t);
        mark(t);
      });
    }

    var main = document.getElementById('rt-main');
    if (!main) return;

    // 2. The shared hover marker.
    var data = null;
    var overlays = [];
    function hoverData() {
      if (data) return data;
      var el = document.getElementById('rt-hover');
      if (!el) return null;
      try { data = JSON.parse(el.textContent); } catch (e) { data = null; }
      return data;
    }
    function clear() {
      for (var i = 0; i < overlays.length; i++) overlays[i].remove();
      overlays = [];
    }
    function add(parent, el) {
      parent.appendChild(el);
      overlays.push(el);
    }
    function show(i, hovered) {
      clear();
      var d = hoverData();
      if (!d || d.n < 2 || i < 0 || i >= d.n) return;
      var left = i / (d.n - 1) * 100;
      var plots = main.querySelectorAll('[data-chart]');
      for (var p = 0; p < plots.length; p++) {
        var plot = plots[p];
        var key = plot.getAttribute('data-chart');
        var xh = make('div', 'xh');
        xh.style.left = left + '%';
        add(plot, xh);
        var c = key === 'route' ? null : (d.c && d.c[key]);
        if (c) {
          c.s.forEach(function (s) {
            if (s.y[i] === null) return;
            var dot = make('span', 'dot f' + s.k);
            dot.style.left = left + '%';
            dot.style.top = s.y[i] + '%';
            add(plot, dot);
          });
        }
        if (plot !== hovered) continue;
        var tip = make('div', 'tip' + (left > 62 ? ' tip-l' : '') + (key === 'route' ? ' tip-below' : ''));
        tip.style.left = left + '%';
        tip.appendChild(make('div', 'tip-t tnum', d.t[i]));
        if (key === 'route') {
          (d.r || []).forEach(function (r) {
            var row = make('div', 'tip-r');
            row.appendChild(make('span', 'sw rs-' + ({ good: 'tunnel', warn: 'own', crit: 'held', unknown: 'nodata' })[r.l[i]]));
            row.appendChild(make('span', 'nm', r.tv));
            row.appendChild(make('strong', null, r.s[i]));
            tip.appendChild(row);
          });
        } else if (c) {
          c.s.forEach(function (s) {
            var row = make('div', 'tip-r');
            row.appendChild(make('span', 'sw f' + s.k));
            row.appendChild(make('span', 'nm', s.name));
            row.appendChild(make('strong', 'tnum', s.v[i]));
            tip.appendChild(row);
          });
          if (c.n[i]) tip.appendChild(make('div', 'tip-n', c.n[i]));
        }
        add(plot, tip);
      }
    }
    document.addEventListener('mousemove', function (e) {
      var plot = e.target.closest && e.target.closest('[data-chart]');
      if (!plot || !main.contains(plot)) {
        if (overlays.length) clear();
        return;
      }
      var d = hoverData();
      var r = plot.getBoundingClientRect();
      if (!d || !r.width) return;
      var f = Math.max(0, Math.min(1, (e.clientX - r.left) / r.width));
      show(Math.round(f * (d.n - 1)), plot);
    });

    // 3. Fresh HTML every 30 seconds, while the page is visible.
    var updated = document.getElementById('rt-updated');
    var busy = false;
    function refresh() {
      if (busy || document.hidden) return;
      busy = true;
      var ctl = typeof AbortController === 'function' ? new AbortController() : null;
      var timer = ctl ? setTimeout(function () { ctl.abort(); }, 10000) : null;
      var range = main.getAttribute('data-range') || '24h';
      fetch('/_dash/fragment?range=' + encodeURIComponent(range), {
        signal: ctl ? ctl.signal : undefined, credentials: 'omit', cache: 'no-store'
      }).then(function (res) {
        if (!res.ok) throw new Error('HTTP ' + res.status);
        return res.text();
      }).then(function (html) {
        clear();
        main.innerHTML = html;
        data = null;
        main.classList.remove('stale');
        var stamp = document.getElementById('rt-stamp');
        if (stamp && updated) updated.textContent = stamp.getAttribute('data-updated');
      }).catch(function () {
        // Keep what's shown, and say it may be old.
        main.classList.add('stale');
        if (updated && updated.textContent.indexOf(' (offline)') < 0) updated.textContent += ' (offline)';
      }).then(function () {
        if (timer) clearTimeout(timer);
        busy = false;
      });
    }
    setInterval(refresh, 30000);
    document.addEventListener('visibilitychange', function () {
      if (!document.hidden) refresh();
    });
  }

  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', start);
  else start();
})();
