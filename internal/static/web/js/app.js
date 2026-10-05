// Progressive enhancement only: the shell works without this script.
(function () {
  var toggle = document.getElementById('menu-toggle');
  var app = document.getElementById('app');
  if (toggle && app) {
    var sidebar = app.querySelector('.sidebar');
    var backdrop = document.getElementById('sidebar-backdrop');

    function sidebarFocusables() {
      if (!sidebar) return [];
      return Array.prototype.slice.call(
        sidebar.querySelectorAll('a[href], button:not([disabled]), [tabindex]:not([tabindex="-1"])')
      );
    }

    function closeSidebar(returnFocus) {
      app.classList.remove('sidebar-open');
      toggle.setAttribute('aria-expanded', 'false');
      if (backdrop) backdrop.hidden = true;
      if (returnFocus) toggle.focus();
    }

    function openSidebar() {
      app.classList.add('sidebar-open');
      toggle.setAttribute('aria-expanded', 'true');
      if (backdrop) backdrop.hidden = false;
      var items = sidebarFocusables();
      if (items.length) items[0].focus();
    }

    toggle.addEventListener('click', function () {
      if (app.classList.contains('sidebar-open')) closeSidebar(true);
      else openSidebar();
    });

    if (backdrop) backdrop.addEventListener('click', function () { closeSidebar(true); });

    // Following a link inside the off-canvas sidebar dismisses it.
    if (sidebar) {
      sidebar.addEventListener('click', function (e) {
        if (e.target && e.target.closest && e.target.closest('a[href]')) closeSidebar(false);
      });
    }

    // Escape closes; Tab stays within the sidebar while it is open.
    document.addEventListener('keydown', function (e) {
      if (!app.classList.contains('sidebar-open')) return;
      if (e.key === 'Escape') { closeSidebar(true); return; }
      if (e.key !== 'Tab') return;
      var items = sidebarFocusables();
      if (!items.length) return;
      var first = items[0];
      var last = items[items.length - 1];
      if (e.shiftKey && document.activeElement === first) {
        e.preventDefault();
        last.focus();
      } else if (!e.shiftKey && document.activeElement === last) {
        e.preventDefault();
        first.focus();
      }
    });
  }

  // Mark the active nav destination for assistive tech.
  document.querySelectorAll('.nav a.active').forEach(function (a) {
    a.setAttribute('aria-current', 'page');
  });

  // Project form: generate a <id>.<server-ip>.sslip.io domain so an app can get
  // a working public hostname without owning DNS yet.
  var genDomain = document.getElementById('generate-domain');
  if (genDomain) {
    genDomain.addEventListener('click', function () {
      var form = genDomain.closest('form');
      var domainEl = document.getElementById('project-domain');
      if (!form || !domainEl) return;
      var sel = form.querySelector('select[name=server_id]');
      var idEl = form.querySelector('[name=id]');
      var opt = sel ? sel.options[sel.selectedIndex] : null;
      var ip = opt ? (opt.getAttribute('data-ip') || '').trim() : '';
      var local = opt ? opt.getAttribute('data-local') === 'true' : false;
      var id = idEl ? (idEl.value || '').trim().toLowerCase().replace(/[^a-z0-9-]+/g, '-').replace(/^-+|-+$/g, '') : '';
      if (!local) {
        window.alert('Automatic sslip.io generation is only available for a local target. Set the domain to the central Caddy host for remote targets.');
        return;
      }
      if (!ip) {
        window.alert('Set the server public IP first (Servers, then edit the server).');
        return;
      }
      if (!id) {
        window.alert('Set the resource ID first.');
        return;
      }
      domainEl.value = id + '.' + ip + '.sslip.io';
      domainEl.dispatchEvent(new Event('input', { bubbles: true }));
    });
  }

  // Server form: show only the connection fields for the chosen type. Without
  // this script both fieldsets stay visible, which still works.
  var serverForm = document.querySelector('[data-server-form]');
  if (serverForm) {
    var type = document.getElementById('server-type');
    var groups = serverForm.querySelectorAll('[data-server-group]');
    var apply = function () {
      var want = type ? type.value : 'docker';
      groups.forEach(function (group) {
        // A group may target several server types (space-separated).
        var names = (group.getAttribute('data-server-group') || '').split(/\s+/);
        group.hidden = names.indexOf(want) === -1;
      });
    };
    if (type) type.addEventListener('change', apply);
    apply();
  }

  // Resource form: show only the field groups relevant to the selected server's
  // type (docker / iis / winsvc). Without this script every group stays visible,
  // which still works. Groups live under the collapsed Advanced block.
  var projectForm = document.querySelector('[data-project-form]');
  if (projectForm) {
    var projectServer = projectForm.querySelector('select[name=server_id]');
    var applyProjectType = function () {
      var opt = projectServer ? projectServer.options[projectServer.selectedIndex] : null;
      var type = opt ? (opt.getAttribute('data-type') || '') : '';
      projectForm.querySelectorAll('[data-field-group]').forEach(function (group) {
        group.hidden = !!type && group.getAttribute('data-field-group') !== type;
      });
    };
    if (projectServer) {
      projectServer.addEventListener('change', applyProjectType);
      applyProjectType();
    }
  }

  // Submit-in-progress: disable the submit control once a real submission is
  // under way so a double-click cannot post twice. Themed confirms mark their
  // form from the dialog's OK handler instead (form.submit() skips listeners).
  function markSubmitting(form) {
    form.setAttribute('aria-busy', 'true');
    form.querySelectorAll('button[type=submit], button:not([type])').forEach(function (b) {
      b.disabled = true;
    });
  }
  document.querySelectorAll('form:not([data-confirm])').forEach(function (form) {
    form.addEventListener('submit', function () { markSubmitting(form); });
  });

  // Themed confirmation for destructive actions. Forms opt in with data-confirm
  // and optionally data-confirm-title / -ok / -require / -variant. Each form also
  // carries an inline onsubmit="return confirm(...)" as the no-JS fallback; when
  // the dialog is available we drop that handler so the two never both fire.
  var dialog = document.getElementById('confirm-dialog');
  if (dialog && typeof dialog.showModal === 'function') {
    var titleEl = document.getElementById('confirm-title');
    var bodyEl = document.getElementById('confirm-body');
    var requireWrap = document.getElementById('confirm-require');
    var requireWord = document.getElementById('confirm-require-word');
    var requireInput = document.getElementById('confirm-require-input');
    var okBtn = document.getElementById('confirm-ok');
    var cancelBtn = document.getElementById('confirm-cancel');
    var pending = null;

    function armRequire(word) {
      if (!word) {
        requireWrap.hidden = true;
        requireInput.value = '';
        okBtn.disabled = false;
        return;
      }
      requireWrap.hidden = false;
      requireWord.textContent = word;
      requireInput.value = '';
      okBtn.disabled = true;
    }

    function openConfirm(form) {
      pending = form;
      titleEl.textContent = form.getAttribute('data-confirm-title') || 'Confirm';
      bodyEl.textContent = form.getAttribute('data-confirm') || '';
      okBtn.textContent = form.getAttribute('data-confirm-ok') || 'Confirm';
      okBtn.className = 'btn ' + (form.getAttribute('data-confirm-variant') === 'primary' ? 'btn-primary' : 'btn-danger');
      armRequire(form.getAttribute('data-confirm-require'));
      dialog.showModal();
      (requireWrap.hidden ? cancelBtn : requireInput).focus();
    }

    cancelBtn.addEventListener('click', function () { pending = null; dialog.close(); });
    dialog.addEventListener('close', function () { pending = null; });
    requireInput.addEventListener('input', function () {
      okBtn.disabled = requireInput.value.trim() !== requireWord.textContent;
    });
    okBtn.addEventListener('click', function () {
      var form = pending;
      pending = null;
      dialog.close();
      if (form) {
        markSubmitting(form);
        form.submit();
      }
    });

    document.querySelectorAll('form[data-confirm]').forEach(function (form) {
      form.onsubmit = null;
      form.addEventListener('submit', function (ev) {
        ev.preventDefault();
        openConfirm(form);
      });
    });
  }
})();
