// Progressive enhancement only: the shell works without this script.
(function () {
  var toggle = document.getElementById('menu-toggle');
  var app = document.getElementById('app');
  if (toggle && app) {
    toggle.addEventListener('click', function () {
      app.classList.toggle('sidebar-open');
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
        group.hidden = group.getAttribute('data-server-group') !== want;
      });
    };
    if (type) type.addEventListener('change', apply);
    apply();
  }
})();
