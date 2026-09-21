// Progressive enhancement for the New Resource wizard. Without this script the
// form renders as a single, fully submittable form; with it, it becomes steps.
(function () {
  var form = document.getElementById('wizard');
  if (!form) return;

  form.classList.add('wizard-js');

  var steps = Array.prototype.slice.call(form.querySelectorAll('.wizard-step'));
  var stepList = Array.prototype.slice.call(document.querySelectorAll('#wizard-steps li'));
  var back = document.getElementById('wz-back');
  var next = document.getElementById('wz-next');
  var submit = document.getElementById('wz-submit');
  var review = document.getElementById('wz-review');
  var idx = 0;

  function family() {
    var r = form.querySelector('input[name=wizard_family]:checked');
    return r ? r.value : 'docker';
  }

  function source() {
    var r = form.querySelector('input[name=source]:checked');
    return r ? r.value : 'dockerfile';
  }

  function stepVisible(step) {
    var fam = step.getAttribute('data-family');
    if (fam && fam !== family()) return false;
    if (step.getAttribute('data-requires-repo') === 'true' && family() === 'docker' && source() === 'image') {
      return false;
    }
    return true;
  }

  function applyConditionals() {
    var fam = family();
    var src = source();

    var serverSelect = form.querySelector('select[name=server_id]');
    if (serverSelect) {
      var firstMatch = null;
      Array.prototype.forEach.call(serverSelect.options, function (opt) {
        var match = opt.getAttribute('data-type') === fam;
        opt.hidden = !match;
        opt.disabled = !match;
        if (match && !firstMatch) firstMatch = opt;
      });
      var current = serverSelect.options[serverSelect.selectedIndex];
      if ((!current || current.disabled) && firstMatch) {
        serverSelect.value = firstMatch.value;
      }
    }

    var iisGroup = form.querySelector('[data-group=iis]');
    if (iisGroup) iisGroup.hidden = fam !== 'iis';
    var winsvcGroup = form.querySelector('[data-group=winsvc]');
    if (winsvcGroup) winsvcGroup.hidden = fam !== 'winsvc';

    // Detection reads Windows paths on the target, so it is Windows-only.
    var detectBox = document.getElementById('wz-detect-box');
    if (detectBox) detectBox.hidden = fam === 'docker';

    form.querySelectorAll('[data-src]').forEach(function (el) {
      var kind = el.getAttribute('data-src');
      if (kind === 'docker') {
        el.hidden = fam !== 'docker';
      } else {
        el.hidden = fam !== 'docker' || src !== kind;
      }
    });

    var port = form.querySelector('[name=ports_exposes]');
    if (port && !port.dataset.touched) port.value = fam === 'iis' ? '80' : '8080';
    var health = form.querySelector('[name=health_path]');
    if (health && !health.dataset.touched) health.value = fam === 'iis' ? '/' : '/healthz';
  }

  function value(name) {
    var el = form.querySelector('[name=' + name + ']');
    return el ? el.value : '';
  }

  function buildReview() {
    if (!review) return;
    var fam = family();
    var src = source();
    var familyLabel = fam === 'iis' ? 'Windows IIS' : fam === 'winsvc' ? 'Windows service (NSSM)' : 'Docker';
    var rows = [
      ['Type', familyLabel],
      ['Source', fam === 'docker' ? src : 'Git repository'],
      ['Name', value('name')],
      ['ID', value('id')],
      ['Server', value('server_id')],
      ['Repository', fam === 'docker' && src === 'image' ? 'prebuilt image' : value('repo_url')],
      ['Domain', value('domain')],
      ['Ports exposes', value('ports_exposes')]
    ];
    review.textContent = '';
    rows.forEach(function (row) {
      var dt = document.createElement('dt');
      dt.textContent = row[0];
      var dd = document.createElement('dd');
      dd.textContent = row[1] || '\u2014';
      review.appendChild(dt);
      review.appendChild(dd);
    });
  }

  function render() {
    applyConditionals();
    var visible = steps.filter(stepVisible);
    if (idx >= visible.length) idx = visible.length - 1;
    if (idx < 0) idx = 0;

    steps.forEach(function (step) { step.hidden = true; });
    if (visible[idx]) visible[idx].hidden = false;

    stepList.forEach(function (li, i) {
      li.hidden = i >= visible.length;
      li.classList.toggle('active', i === idx);
    });

    var last = idx === visible.length - 1;
    back.disabled = idx === 0;
    next.hidden = last;
    submit.hidden = !last;
    buildReview();
  }

  next.addEventListener('click', function () {
    idx = Math.min(idx + 1, steps.filter(stepVisible).length - 1);
    render();
  });
  back.addEventListener('click', function () {
    idx = Math.max(idx - 1, 0);
    render();
  });

  form.querySelectorAll('input[name=wizard_family]').forEach(function (r) {
    r.addEventListener('change', function () { idx = 0; render(); });
  });
  form.querySelectorAll('input[name=source]').forEach(function (r) {
    r.addEventListener('change', render);
  });
  var serverSelect = form.querySelector('select[name=server_id]');
  if (serverSelect) serverSelect.addEventListener('change', buildReview);

  var nameInput = form.querySelector('[name=name]');
  var idInput = form.querySelector('[name=id]');
  if (nameInput && idInput) {
    var idTouched = false;
    idInput.addEventListener('input', function () { idTouched = true; });
    nameInput.addEventListener('input', function () {
      if (idTouched) return;
      idInput.value = nameInput.value.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '');
    });
  }
  ['ports_exposes', 'health_path'].forEach(function (n) {
    var el = form.querySelector('[name=' + n + ']');
    if (el) el.addEventListener('input', function () { el.dataset.touched = '1'; });
  });

  // ---- Repository detection ----

  var detectBtn = document.getElementById('wz-detect');
  var detectStatus = document.getElementById('wz-detect-status');
  var runtimeSelect = document.getElementById('wz-runtime');

  function setStatus(message, isError) {
    if (!detectStatus) return;
    detectStatus.textContent = message;
    detectStatus.classList.toggle('error', !!isError);
  }

  function setField(name, v) {
    var el = form.querySelector('[name=' + name + ']');
    if (el && v) {
      el.value = v;
      el.dataset.touched = '1';
    }
  }

  function absoluteExe(exe, workDir) {
    if (!exe) return '';
    if (/^[A-Za-z]:/.test(exe) || exe.indexOf('\\\\') === 0) return exe;
    return workDir ? workDir.replace(/[\\/]+$/, '') + '\\' + exe : exe;
  }

  function applyPlan(plan) {
    var id = value('id') || value('name') || 'app';
    var slug = id.replace(/[^A-Za-z0-9]+/g, '') || 'app';

    var workEl = form.querySelector('[name=service_work_dir]');
    var workDir = workEl && workEl.value ? workEl.value : 'C:\\ProgramData\\winify\\apps\\' + id;

    var nameEl = form.querySelector('[name=service_name]');
    if (nameEl && !nameEl.value) nameEl.value = slug;
    if (workEl && !workEl.value) workEl.value = workDir;

    setField('service_build_command', plan.build_command);
    setField('service_exe', absoluteExe(plan.exe, workDir));
    setField('service_args', plan.args);
    setField('service_source_subdir', plan.source_subdir);
    if (plan.port) setField('ports_exposes', String(plan.port));
    if (plan.health_path) setField('health_path', plan.health_path);
    if (plan.caddy_mode) setField('caddy_mode', plan.caddy_mode);
    if (runtimeSelect && plan.language) runtimeSelect.value = plan.language;

    var parts = [];
    if (plan.framework) parts.push(plan.framework);
    if (plan.evidence && plan.evidence.length) {
      parts.push('from ' + plan.evidence.map(function (e) { return e.path; }).join(', '));
    }
    if (plan.confidence) parts.push(plan.confidence + ' confidence');
    setStatus('Detected ' + plan.language + ' \u2014 ' + parts.join(' \u00b7 '), false);
    buildReview();
  }

  if (detectBtn) {
    detectBtn.addEventListener('click', function () {
      var repo = value('repo_url');
      var server = value('server_id');
      if (!repo) { setStatus('Enter a repository URL first.', true); return; }
      if (!server) { setStatus('Choose a target server first.', true); return; }

      var body = new URLSearchParams();
      body.set('repo_url', repo);
      body.set('branch', value('branch'));
      body.set('server_id', server);
      if (runtimeSelect && runtimeSelect.value) body.set('language', runtimeSelect.value);

      detectBtn.disabled = true;
      setStatus('Cloning and inspecting the repository on the target\u2026', false);
      fetch('/projects/detect', {
        method: 'POST',
        headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
        body: body.toString()
      }).then(function (r) {
        return r.json().then(function (data) { return { ok: r.ok, data: data }; });
      }).then(function (res) {
        if (!res.ok) { setStatus(res.data.error || 'Detection failed.', true); return; }
        if (!res.data.detected) {
          setStatus('No known language detected \u2014 set the fields manually.', true);
          return;
        }
        applyPlan(res.data.plan);
      }).catch(function (err) {
        setStatus('Detection failed: ' + err, true);
      }).finally(function () {
        detectBtn.disabled = false;
      });
    });
  }

  form.addEventListener('input', buildReview);
  render();
})();
