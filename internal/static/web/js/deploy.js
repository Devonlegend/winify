// One-click deploys with a live log drawer. Without this script, deploy forms
// still submit normally and redirect to the deployment history.
(function () {
  var drawer = document.getElementById('deploy-drawer');
  if (!drawer) return;

  var logEl = document.getElementById('drawer-log');
  var statusEl = document.getElementById('drawer-status');
  var linkEl = document.getElementById('drawer-link');
  var closeBtn = document.getElementById('drawer-close');
  var source = null;
  var reloadOnFinish = false;

  function setStatus(text, kind) {
    statusEl.textContent = text;
    statusEl.className = 'status' + (kind ? ' status-' + kind : '');
  }

  function close() {
    if (source) source.close();
    drawer.hidden = true;
  }

  function appendLog(chunk) {
    logEl.textContent += chunk;
    logEl.scrollTop = logEl.scrollHeight;
  }

  function stream(deploymentId) {
    if (source) source.close();
    source = new EventSource('/deployments/' + deploymentId + '/stream');
    source.addEventListener('log', function (e) {
      appendLog(JSON.parse(e.data).chunk);
    });
    source.addEventListener('status', function (e) {
      var d = JSON.parse(e.data);
      setStatus(d.status, d.status);
      if (d.status === 'success' || d.status === 'failed') {
        if (d.error) appendLog('\n' + d.error);
        source.close();
        source = null;
        if (reloadOnFinish) setTimeout(function () { window.location.reload(); }, 700);
      }
    });
    source.addEventListener('error', function () {
      source.close();
      source = null;
    });
  }

  function open(projectId, reload) {
    reloadOnFinish = reload !== false;
    drawer.hidden = false;
    logEl.textContent = '';
    setStatus('starting', 'running');
    linkEl.href = '/projects/' + encodeURIComponent(projectId);

    fetch('/deployments/start/' + encodeURIComponent(projectId), {
      method: 'POST',
      headers: { 'Accept': 'application/json' }
    })
      .then(function (r) {
        return r.json().catch(function () { return {}; }).then(function (d) { return { ok: r.ok, data: d }; });
      })
      .then(function (res) {
        if (!res.ok) {
          setStatus('failed', 'failed');
          appendLog(res.data.error || 'Could not start the deployment.');
          return;
        }
        stream(res.data.deployment_id);
      })
      .catch(function () {
        setStatus('failed', 'failed');
        appendLog('Could not reach the server.');
      });
  }

  if (closeBtn) closeBtn.addEventListener('click', close);

  document.querySelectorAll('form[data-deploy]').forEach(function (form) {
    form.addEventListener('submit', function (ev) {
      ev.preventDefault();
      open(form.getAttribute('data-deploy'));
    });
  });
  document.querySelectorAll('[data-deploy-open]').forEach(function (btn) {
    btn.addEventListener('click', function (ev) {
      ev.preventDefault();
      open(btn.getAttribute('data-deploy-open'));
    });
  });

  window.deployDrawer = { open: open };
})();
