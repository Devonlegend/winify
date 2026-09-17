// Progressive enhancement only: the shell works without this script.
(function () {
  var toggle = document.getElementById('menu-toggle');
  var app = document.getElementById('app');
  if (toggle && app) {
    toggle.addEventListener('click', function () {
      app.classList.toggle('sidebar-open');
    });
  }

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
      var id = idEl ? (idEl.value || '').trim().toLowerCase().replace(/[^a-z0-9-]+/g, '-').replace(/^-+|-+$/g, '') : '';
      if (!ip) {
        window.alert('Set the server public IP first (Servers, then edit the server).');
        return;
      }
      if (!id) {
        window.alert('Set the resource ID first.');
        return;
      }
      domainEl.value = id + '.' + ip + '.sslip.io';
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
})();
