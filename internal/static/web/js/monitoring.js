// Monitoring tab: renders gauges from the latest sample and a Chart.js history
// per server. Auto-refreshes; when a server is unreachable it marks the card
// "down" and shows the connection error instead of leaving stale numbers.
(function () {
  var root = document.querySelector('main[data-refresh-seconds]');
  if (!root) return;

  var intervalSec = parseInt(root.getAttribute('data-refresh-seconds'), 10) || 30;
  var pollMs = Math.max(5000, intervalSec * 1000);
  var entries = {};
  var pollCount = 0;

  function fmtPct(v) {
    return (typeof v === 'number' ? v : 0).toFixed(1) + '%';
  }

  function fmtUptime(s) {
    if (!s) return '--';
    var d = Math.floor(s / 86400);
    var h = Math.floor((s % 86400) / 3600);
    var m = Math.floor((s % 3600) / 60);
    if (d) return d + 'd ' + h + 'h';
    if (h) return h + 'h ' + m + 'm';
    return m + 'm';
  }

  function fmtAgo(iso) {
    if (!iso) return '--';
    var t = new Date(iso).getTime();
    if (!t) return '--';
    var s = Math.max(0, Math.round((Date.now() - t) / 1000));
    if (s < 60) return s + 's ago';
    if (s < 3600) return Math.round(s / 60) + 'm ago';
    return Math.round(s / 3600) + 'h ago';
  }

  function initChart(card) {
    var canvas = card.querySelector('canvas[data-role=chart]');
    if (!canvas || typeof Chart === 'undefined') return null;
    return new Chart(canvas, {
      type: 'line',
      data: {
        labels: [],
        datasets: [
          { label: 'CPU %', data: [], borderColor: '#38bdf8', backgroundColor: 'rgba(56,189,248,0.12)', tension: 0.3, pointRadius: 0, borderWidth: 2 },
          { label: 'Mem %', data: [], borderColor: '#34d399', backgroundColor: 'rgba(52,211,153,0.10)', tension: 0.3, pointRadius: 0, borderWidth: 2 },
          { label: 'Disk %', data: [], borderColor: '#c4b5fd', backgroundColor: 'rgba(196,181,253,0.10)', tension: 0.3, pointRadius: 0, borderWidth: 2 }
        ]
      },
      options: {
        responsive: true,
        maintainAspectRatio: false,
        animation: false,
        scales: {
          y: { min: 0, max: 100, ticks: { color: '#64748b', callback: function (v) { return v + '%'; } }, grid: { color: '#1e293b' } },
          x: { ticks: { color: '#64748b', maxTicksLimit: 6 }, grid: { display: false } }
        },
        plugins: { legend: { labels: { color: '#94a3b8', boxWidth: 10, boxHeight: 10 } } }
      }
    });
  }

  function loadHistory(card, chart) {
    if (!chart) return;
    var id = card.getAttribute('data-server-id');
    fetch('/api/servers/' + encodeURIComponent(id) + '/metrics')
      .then(function (r) { return r.ok ? r.json() : []; })
      .then(function (rows) {
        if (!Array.isArray(rows)) return;
        chart.data.labels = rows.map(function (m) { return new Date(m.ts).toLocaleTimeString(); });
        chart.data.datasets[0].data = rows.map(function (m) { return m.cpu_percent || 0; });
        chart.data.datasets[1].data = rows.map(function (m) { return m.mem_percent || 0; });
        chart.data.datasets[2].data = rows.map(function (m) { return m.disk_percent || 0; });
        chart.update('none');
      })
      .catch(function () {});
  }

  function setGauge(card, role, pct) {
    var val = card.querySelector('[data-role=' + role + ']');
    var bar = card.querySelector('[data-role=' + role + '-bar]');
    if (val) val.textContent = fmtPct(pct);
    if (bar) {
      var clamped = Math.min(100, Math.max(0, pct));
      bar.style.width = clamped + '%';
      bar.setAttribute('data-level', pct >= 90 ? 'crit' : pct >= 75 ? 'warn' : 'ok');
    }
  }

  function renderServices(card, services) {
    var box = card.querySelector('[data-role=services]');
    if (!box) return;
    box.innerHTML = '';
    (services || []).forEach(function (s) {
      var chip = document.createElement('span');
      var up = s.state === 'active' || s.state === 'Running' || s.state === 'running';
      chip.className = 'svc ' + (up ? 'svc-ok' : 'svc-bad');
      chip.textContent = s.name + ': ' + s.state;
      box.appendChild(chip);
    });
  }

  function renderCard(entry, m) {
    var card = entry.card;
    var stale = !!(m && m.ts && (Date.now() - new Date(m.ts).getTime()) > intervalSec * 2000);
    var state = !m ? 'waiting' : (!m.reachable ? 'down' : (stale ? 'stale' : 'ok'));
    card.setAttribute('data-state', state);

    var pill = card.querySelector('[data-role=status]');
    if (pill) { pill.textContent = state; pill.className = 'pill pill-' + state; }

    var err = card.querySelector('[data-role=error]');
    if (err) {
      if (m && !m.reachable && m.error) {
        err.textContent = 'Unreachable: ' + m.error;
        err.hidden = false;
      } else {
        err.hidden = true;
        err.textContent = '';
      }
    }

    setGauge(card, 'cpu', m ? m.cpu_percent : 0);
    setGauge(card, 'mem', m ? m.mem_percent : 0);
    setGauge(card, 'disk', m ? m.disk_percent : 0);

    var up = card.querySelector('[data-role=uptime]');
    if (up) up.textContent = m ? fmtUptime(m.uptime_seconds) : '--';
    var load = card.querySelector('[data-role=load]');
    if (load) load.textContent = m ? (m.load1 || 0).toFixed(2) : '--';
    var upd = card.querySelector('[data-role=updated]');
    if (upd) upd.textContent = m ? fmtAgo(m.ts) : '--';

    renderServices(card, m ? m.services : []);
  }

  Array.prototype.slice.call(root.querySelectorAll('.metric-card')).forEach(function (card) {
    var chart = initChart(card);
    entries[card.getAttribute('data-server-id')] = { card: card, chart: chart };
    loadHistory(card, chart);
  });

  function poll() {
    fetch('/api/metrics')
      .then(function (r) { return r.ok ? r.json() : []; })
      .then(function (rows) {
        var byId = {};
        (rows || []).forEach(function (m) { byId[m.server_id] = m; });
        Object.keys(entries).forEach(function (id) {
          renderCard(entries[id], byId[id]);
        });
        pollCount++;
        if (pollCount % 4 === 0) {
          Object.keys(entries).forEach(function (id) {
            loadHistory(entries[id].card, entries[id].chart);
          });
        }
      })
      .catch(function () {});
  }

  poll();
  setInterval(poll, pollMs);
})();
