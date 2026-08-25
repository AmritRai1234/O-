// Live-reload client injected by serve. Polls /__o_reload for the newest
// mtime under the served root and reloads the page when it changes.
(function () {
  var last = 0;
  function poll() {
    fetch("/__o_reload", { cache: "no-store" })
      .then(function (r) { return r.json(); })
      .then(function (d) {
        if (last !== 0 && d.mtime > last) {
          location.reload();
          return;
        }
        last = d.mtime;
      })
      .catch(function () {})
      .finally(function () { setTimeout(poll, 700); });
  }
  poll();
})();
