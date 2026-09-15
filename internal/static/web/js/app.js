// Progressive enhancement only: the shell works without this script.
(function () {
  var toggle = document.getElementById('menu-toggle');
  var app = document.getElementById('app');
  if (toggle && app) {
    toggle.addEventListener('click', function () {
      app.classList.toggle('sidebar-open');
    });
  }
})();
