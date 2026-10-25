// orbit dev live reload (PLAN.md §7.1): reload the page on every "reload"
// event. If the connection drops, reconnect with backoff, and reload once
// it's back (orbit dev restarted, or the page missed an event).
(() => {
  if (window.__orbitReload) return;
  window.__orbitReload = true;
  let delay = 250;
  let lost = false;
  const connect = () => {
    const es = new EventSource("/__orbit/events");
    es.addEventListener("reload", () => location.reload());
    es.onopen = () => {
      if (lost) location.reload();
      delay = 250;
    };
    es.onerror = () => {
      es.close();
      lost = true;
      setTimeout(connect, delay);
      delay = Math.min(delay * 2, 5000);
    };
  };
  connect();
})();
