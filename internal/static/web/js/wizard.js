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

    form.querySelectorAll('[data-src]').forEach(function (el) {
      var kind = el.getAttribute('data-src');
      if (kind === 'docker') {
        el.hidden = fam !== 'docker';
      } else {
        el.hidden = fam !== 'docker' || src !== kind;
      }
    });

    var port = form.querySelector('[name=port]');
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
    var rows = [
      ['Type', fam === 'iis' ? 'Windows IIS' : 'Docker'],
      ['Source', fam === 'iis' ? 'Git repository' : src],
      ['Name', value('name')],
      ['ID', value('id')],
      ['Server', value('server_id')],
      ['Repository', fam === 'docker' && src === 'image' ? 'prebuilt image' : value('repo_url')],
      ['Domain', value('domain')],
      ['Port', value('port')]
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
  ['port', 'health_path'].forEach(function (n) {
    var el = form.querySelector('[name=' + n + ']');
    if (el) el.addEventListener('input', function () { el.dataset.touched = '1'; });
  });

  form.addEventListener('input', buildReview);
  render();
})();
