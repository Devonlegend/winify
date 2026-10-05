// One-click deploys with a live log drawer. Without this script, deploy forms
// still submit normally and redirect to the deployment history.
(function () {
  var drawer = document.getElementById('deploy-drawer');
  if (!drawer) return;

  var logEl = document.getElementById('drawer-log');
  var statusEl = document.getElementById('drawer-status');
  var linkEl = document.getElementById('drawer-link');
  var closeBtn = document.getElementById('drawer-close');
  var reopenBtn = document.getElementById('deploy-reopen');
  var reopenStatus = document.getElementById('deploy-reopen-status');
  var source = null;
  var reloadOnFinish = false;
  var streaming = false;

  function setStatus(text, kind) {
    var cls = 'status' + (kind ? ' status-' + kind : '');
    statusEl.textContent = text;
    statusEl.className = cls;
    if (reopenStatus) {
      reopenStatus.textContent = text;
      reopenStatus.className = cls;
    }
  }

  // close hides the drawer but keeps streaming, so a long deploy stays visible
  // as a persistent pill the operator can reopen.
  function close() {
    drawer.hidden = true;
    if (streaming && reopenBtn) reopenBtn.hidden = false;
  }

  function reopen() {
    drawer.hidden = false;
    if (reopenBtn) reopenBtn.hidden = true;
  }

  function appendLog(chunk) {
    logEl.textContent += chunk;
    logEl.scrollTop = logEl.scrollHeight;
  }

  function stream(deploymentId) {
    if (source) source.close();
    streaming = true;
    if (reopenBtn) reopenBtn.hidden = true;
    source = new EventSource('/deployments/' + deploymentId + '/stream');
    source.addEventListener('log', function (e) {
      appendLog(JSON.parse(e.data).chunk);
    });
    source.addEventListener('status', function (e) {
      var d = JSON.parse(e.data);
      setStatus(d.status, d.status);
      if (d.status === 'success' || d.status === 'failed' || d.status === 'unknown') {
        if (d.error) appendLog('\n' + d.error);
        source.close();
        source = null;
        streaming = false;
        if (reopenBtn) reopenBtn.hidden = true;
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
    if (reopenBtn) reopenBtn.hidden = true;
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
          // A deploy is already running: attach to it instead of dead-ending.
          if (res.data && res.data.deployment_id) {
            appendLog('A deployment is already running for this project \u2014 showing its live log.\n');
            stream(res.data.deployment_id);
            return;
          }
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
  if (reopenBtn) reopenBtn.addEventListener('click', reopen);

  document.querySelectorAll('form[data-deploy]').forEach(function (form) {
    form.addEventListener('submit', function (ev) {
      ev.preventDefault();
      open(form.getAttribute('data-deploy'));
    });
  });
})();
