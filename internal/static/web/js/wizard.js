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
  // prefilled marks a form re-opened with existing values (validation error or
  // edit): family defaults must not overwrite what the operator already set.
  var prefilled = form.getAttribute('data-prefilled') === 'true';

  function family() {
    var r = form.querySelector('input[name=wizard_family]:checked');
    return r ? r.value : 'winsvc';
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
    if (port && !port.dataset.touched && !prefilled) port.value = fam === 'iis' ? '80' : '8080';
    var health = form.querySelector('[name=health_path]');
    if (health && !health.dataset.touched && !prefilled) health.value = fam === 'iis' ? '/' : '/healthz';

    // Copy follows the target: the same field is a container port, a service
    // listen port, or an IIS site port, and the helper text names what winify
    // will actually do on that host.
    var portLabel = form.querySelector('[data-label-ports]');
    if (portLabel) {
      portLabel.textContent = fam === 'docker' ? 'Container port' : fam === 'iis' ? 'Site port' : 'Listening port';
    }
    var targetHelp = form.querySelector('[data-target-help]');
    if (targetHelp) {
      targetHelp.textContent = fam === 'docker'
        ? 'Pick the Linux host that runs Docker. winify builds the image and runs the container there.'
        : fam === 'iis'
        ? 'Pick the Windows host. winify publishes the built files to an IIS site behind Caddy.'
        : 'Pick the Windows host. winify installs the executable as a service (NSSM) with Caddy in front.';
    }
    var advHint = form.querySelector('[data-advanced-hint]');
    if (advHint) {
      advHint.textContent = fam === 'docker'
        ? '\u2014 Dockerfile, Compose and build'
        : fam === 'iis'
        ? '\u2014 IIS paths, build command and env'
        : '\u2014 service paths, build command and env';
    }
  }

  function value(name) {
    var el = form.querySelector('[name=' + name + ']');
    return el ? el.value : '';
  }

  // goToStep jumps the wizard to the step with the given data-step number.
  // idx indexes the *visible* step list, so map through stepVisible first.
  function goToStep(n) {
    var visible = steps.filter(stepVisible);
    for (var i = 0; i < visible.length; i++) {
      if (Number(visible[i].getAttribute('data-step')) === n) {
        idx = i;
        render();
        if (typeof form.scrollIntoView === 'function') {
          form.scrollIntoView({ behavior: 'smooth', block: 'start' });
        }
        return;
      }
    }
  }

  var runtimeNames = { python: 'Python', node: 'Node', go: 'Go', dotnet: '.NET', static: 'Static HTML' };

  function runtimeLabel(r) {
    return runtimeNames[r] || (r ? r : 'Auto-detect');
  }

  function sourceLabel(s) {
    return s === 'compose' ? 'Docker Compose' : s === 'image' ? 'Docker image' : 'Git repository';
  }

  function field(name) {
    var e = form.querySelector('[name=' + name + ']');
    return e ? String(e.value || '').trim() : '';
  }

  function isChecked(name) {
    var e = form.querySelector('[name=' + name + ']');
    return !!(e && e.checked);
  }

  function envCount(name) {
    var e = form.querySelector('[name=' + name + ']');
    if (!e || !e.value) return 0;
    return e.value.split(/\r?\n/).filter(function (line) { return line.trim() !== ''; }).length;
  }

  function el(tag, className, text) {
    var node = document.createElement(tag);
    if (className) node.className = className;
    if (text) node.textContent = text;
    return node;
  }

  // addRow appends one label/value pair. An empty value renders as an em dash
  // rather than collapsing the row, so the manifest never hides a gap.
  function addRow(list, row) {
    if (row.skip) return;
    var dt = el('dt', null, row.label);
    var dd = el('dd');
    if (row.value) {
      dd.appendChild(el('span', row.mono ? 'mono' : null, row.value));
    } else if (!row.note) {
      dd.appendChild(el('span', 'review-empty', '\u2014'));
    }
    if (row.extra) dd.appendChild(row.extra);
    if (row.note) dd.appendChild(el('span', 'review-note', row.note));
    list.appendChild(dt);
    list.appendChild(dd);
  }

  function makeEdit(step, label) {
    var b = el('button', 'btn btn-ghost btn-sm review-edit', 'Edit');
    b.type = 'button';
    b.setAttribute('aria-label', 'Edit ' + label.toLowerCase());
    b.addEventListener('click', function () { goToStep(step); });
    return b;
  }

  // domainHint carries the last generate-domain failure so the review can explain
  // it in place instead of an alert.
  var domainHint = '';

  // generateDomain fills the domain field with a free <id>.<server-ip>.sslip.io
  // name for the local target, then refreshes the review so the operator sees it.
  function generateDomain() {
    var sel = form.querySelector('select[name=server_id]');
    var opt = sel ? sel.options[sel.selectedIndex] : null;
    var ip = opt ? String(opt.getAttribute('data-ip') || '').trim() : '';
    var local = opt ? opt.getAttribute('data-local') === 'true' : false;
    var id = field('id') || field('name');
    domainHint = '';
    if (!local) {
      domainHint = 'Automatic generation is only for the local target \u2014 type the domain instead.';
    } else if (!ip) {
      domainHint = 'Set this server\u2019s public IP first (Servers \u2192 edit).';
    } else if (!id) {
      domainHint = 'Set the resource ID first.';
    } else {
      var slug = id.toLowerCase().replace(/[^a-z0-9-]+/g, '-').replace(/^-+|-+$/g, '');
      var input = form.querySelector('[name=domain]');
      if (input) {
        input.value = slug + '.' + ip + '.sslip.io';
        input.dispatchEvent(new Event('input', { bubbles: true }));
      }
    }
    buildReview();
  }

  function makeGenerateButton() {
    var b = el('button', 'btn btn-ghost btn-sm review-generate',
      field('domain') ? 'Generate new' : 'Generate sslip.io domain');
    b.type = 'button';
    b.setAttribute('aria-label', 'Generate a free sslip.io domain name');
    b.addEventListener('click', generateDomain);
    return b;
  }

  // group renders a titled manifest section with an Edit jump back to its step.
  function group(title, step, rows) {
    var section = el('section', 'review-group');
    var head = el('div', 'review-group-head');
    head.appendChild(el('h3', 'review-title', title));
    head.appendChild(makeEdit(step, title));
    section.appendChild(head);
    var list = el('dl', 'review-list');
    rows.forEach(function (row) { addRow(list, row); });
    section.appendChild(list);
    return section;
  }

  function buildReview() {
    if (!review) return;
    review.textContent = '';

    var fam = family();
    var src = source();
    var isImage = fam === 'docker' && src === 'image';
    var familyLabel = fam === 'iis' ? 'IIS (advanced/legacy)' : fam === 'winsvc' ? 'Windows native service' : fam === 'docker' ? 'Docker' : fam;

    // What: identity.
    review.appendChild(group('Resource', 1, [
      { label: 'Type', value: familyLabel },
      { label: 'Source', value: fam === 'docker' ? sourceLabel(src) : 'Git repository' },
      { label: 'Name', value: field('name') },
      { label: 'ID', value: field('id'), mono: true }
    ]));

    // Where it comes from.
    if (!isImage) {
      review.appendChild(group('Repository', 3, [
        { label: 'Repository', value: field('repo_url'), mono: true },
        { label: 'Branch', value: field('branch') || 'main', mono: true },
        { label: 'GitHub repo', value: field('github_repo'), mono: true, skip: !field('github_repo') },
        { label: 'Git credential', value: field('git_credential_ref'), mono: true, skip: !field('git_credential_ref') },
        { label: 'Webhook', value: field('webhook_secret_ref'), mono: true, skip: !field('webhook_secret_ref') }
      ]));
    }

    // Where it runs. The domain is shown as the outcome: point the name, and
    // winify registers the route and issues the certificate itself.
    var domain = field('domain');
    var domainNote = domainHint || (domain
      ? 'HTTPS via Caddy \u00b7 automatic certificate'
      : 'Reachable on the server only \u2014 or generate a free sslip.io name.');
    review.appendChild(group('Where it runs', 4, [
      { label: 'Target server', value: field('server_id'), mono: true },
      { label: 'Domain', value: domain, mono: true, note: domainNote, extra: makeGenerateButton() }
    ]));

    // How it builds and runs: family-specific.
    var how = [];
    if (fam === 'docker') {
      if (src === 'dockerfile') how.push({ label: 'Dockerfile', value: field('dockerfile_path') || 'Dockerfile', mono: true });
      if (src === 'compose') how.push({ label: 'Compose file', value: field('compose_path') || 'docker-compose.yml', mono: true });
      if (src === 'image') how.push({ label: 'Image', value: field('image'), mono: true });
      how.push({ label: 'Container port', value: field('ports_exposes') });
      how.push({ label: 'Ports mappings', value: field('ports_mappings'), mono: true, skip: !field('ports_mappings') });
    } else if (fam === 'iis') {
      how.push({ label: 'Language', value: runtimeLabel(field('runtime')) });
      how.push({ label: 'Build command', value: field('iis_build_command'), mono: true, skip: !field('iis_build_command') });
      how.push({ label: 'Physical path', value: field('iis_physical_path'), mono: true });
      how.push({ label: 'Site', value: field('iis_site') || field('id') });
      how.push({ label: 'App pool', value: field('iis_app_pool') || field('id') });
      how.push({ label: 'Blue-green', value: isChecked('iis_blue_green') ? 'on' : 'off' });
      how.push({ label: 'Site port', value: field('ports_exposes') });
    } else {
      how.push({ label: 'Language', value: runtimeLabel(field('runtime')) });
      how.push({ label: 'Build command', value: field('service_build_command'), mono: true, skip: !field('service_build_command') });
      how.push({ label: 'Executable', value: field('service_exe'), mono: true, skip: !field('service_exe') });
      how.push({ label: 'Arguments', value: field('service_args'), mono: true, skip: !field('service_args') });
      how.push({ label: 'Install dir', value: field('service_work_dir'), mono: true });
      how.push({ label: 'Listening port', value: field('ports_exposes') });
      how.push({ label: 'Caddy mode', value: field('caddy_mode') === 'static' ? 'static files' : '', skip: field('caddy_mode') !== 'static' });
    }
    how.push({ label: 'Health path', value: field('health_path'), mono: true, skip: isChecked('disable_health_check') });
    review.appendChild(group('How it builds & runs', 5, how));

    // Environment: counted, never invisible.
    var runtimeVars = envCount('env');
    var buildVars = envCount('build_env');
    var envValue = runtimeVars + buildVars === 0 ? '' :
      runtimeVars + ' runtime' + (buildVars ? ', ' + buildVars + ' build' : '');
    review.appendChild(group('Environment', 6, [
      { label: 'Variables', value: envValue, note: envValue ? 'set in Advanced options' : 'none set' }
    ]));

    // Focal line: the last thing read before committing.
    var dest = el('p', 'review-destination');
    var name = field('name') || field('id') || 'this resource';
    dest.appendChild(document.createTextNode('Creates '));
    dest.appendChild(el('strong', null, name));
    if (domain) {
      dest.appendChild(document.createTextNode(', serves '));
      dest.appendChild(el('strong', 'mono', domain));
      dest.appendChild(document.createTextNode(' over HTTPS.'));
    } else {
      dest.appendChild(document.createTextNode(' on '));
      dest.appendChild(el('strong', 'mono', field('server_id') || 'the target server'));
      dest.appendChild(document.createTextNode('.'));
    }
    review.appendChild(dest);
  }

  function render() {
    applyConditionals();
    var visible = steps.filter(stepVisible);
    if (idx >= visible.length) idx = visible.length - 1;
    if (idx < 0) idx = 0;

    steps.forEach(function (step) { step.hidden = true; });
    if (visible[idx]) visible[idx].hidden = false;

    // Key the rail by data-step, not position: a Windows target drops the
    // Docker-only Source step, and a Docker "image" source drops Repository.
    // Matching by index would label the wrong step and hide Review.
    var activeStep = visible[idx] ? Number(visible[idx].getAttribute('data-step')) : -1;
    stepList.forEach(function (li) {
      var n = Number(li.getAttribute('data-step'));
      var shown = visible.some(function (s) { return Number(s.getAttribute('data-step')) === n; });
      li.hidden = !shown;
      li.classList.toggle('active', shown && n === activeStep);
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
    r.addEventListener('change', function () { domainHint = ''; idx = 0; render(); });
  });
  form.querySelectorAll('input[name=source]').forEach(function (r) {
    r.addEventListener('change', render);
  });
  var serverSelect = form.querySelector('select[name=server_id]');
  if (serverSelect) serverSelect.addEventListener('change', function () { domainHint = ''; buildReview(); });

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

  function setFieldIfEmpty(name, v) {
    var el = form.querySelector('[name=' + name + ']');
    if (el && v && !el.value) {
      el.value = v;
      el.dataset.touched = '1';
    }
  }

  // selectFamily switches the wizard's target type and re-filters the server
  // list to match.
  function selectFamily(fam) {
    var radio = form.querySelector('input[name=wizard_family][value=' + fam + ']');
    if (radio && !radio.checked) {
      radio.checked = true;
      applyConditionals();
    }
  }

  // serverAvailable reports whether any server matches the current family.
  function serverAvailable() {
    var sel = form.querySelector('select[name=server_id]');
    if (!sel) return false;
    for (var i = 0; i < sel.options.length; i++) {
      if (!sel.options[i].disabled) return true;
    }
    return false;
  }

  function applyPlan(plan) {
    var id = value('id') || value('name') || 'app';
    var slug = id.replace(/[^A-Za-z0-9]+/g, '') || 'app';

    // The detector picks the mechanism: run it as a service, or publish it to IIS.
    if (plan.target) selectFamily(plan.target);

    if (plan.target === 'iis') {
      setFieldIfEmpty('iis_physical_path', 'C:\\inetpub\\wwwroot\\' + id);
      setFieldIfEmpty('iis_site', id);
      setFieldIfEmpty('iis_app_pool', id);
      setField('iis_build_command', plan.build_command);
      setField('iis_source_subdir', plan.source_subdir);
    } else {
      var workEl = form.querySelector('[name=service_work_dir]');
      var workDir = workEl && workEl.value ? workEl.value : 'C:\\ProgramData\\winify\\apps\\' + id;
      var nameEl = form.querySelector('[name=service_name]');
      if (nameEl && !nameEl.value) nameEl.value = slug;
      if (workEl && !workEl.value) workEl.value = workDir;
      setField('service_build_command', plan.build_command);
      setField('service_exe', absoluteExe(plan.exe, workDir));
      setField('service_args', plan.args);
      setField('service_source_subdir', plan.source_subdir);
      if (plan.caddy_mode) setField('caddy_mode', plan.caddy_mode);
    }

    if (plan.port) setField('ports_exposes', String(plan.port));
    if (plan.health_path) setField('health_path', plan.health_path);
    if (runtimeSelect && plan.language) runtimeSelect.value = plan.language;

    var parts = [];
    if (plan.framework) parts.push(plan.framework);
    if (plan.evidence && plan.evidence.length) {
      parts.push('from ' + plan.evidence.map(function (e) { return e.path; }).join(', '));
    }
    if (plan.confidence) parts.push(plan.confidence + ' confidence');

    var where = plan.target === 'iis' ? 'IIS' : 'Windows service';
    var msg = 'Detected ' + plan.language + ' \u2192 ' + where + ' \u2014 ' + parts.join(' \u00b7 ');
    if (!serverAvailable()) {
      setStatus(msg + '. No ' + where + ' server is configured \u2014 add one under Servers.', true);
    } else {
      setStatus(msg, false);
    }
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
      body.set('git_credential_ref', value('git_credential_ref'));
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

  // Clicking a step in the rail jumps straight to it.
  stepList.forEach(function (li) {
    li.addEventListener('click', function () {
      var n = Number(li.getAttribute('data-step'));
      if (n) goToStep(n);
    });
  });

  form.addEventListener('input', buildReview);
  // A prefilled form (edit or validation error) opens on Review.
  if (prefilled) idx = 999;
  render();
})();
