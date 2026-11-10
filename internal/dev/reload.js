// lyr dev live reload (PLAN.md §7.1): reload the page on every "reload"
// event. If the connection drops, reconnect with backoff, and reload once
// it's back (lyr dev restarted, or the page missed an event). A page
// leaving for the back/forward cache closes its stream (open ones would
// use up the browser's 6 connections per host and stall the next page),
// and reloads when restored, since it may have missed an event.
(() => {
  if (window.__lyrReload) return;
  window.__lyrReload = true;
  let delay = 250;
  let lost = false;
  let es;
  const connect = () => {
    es = new EventSource("/__lyr/events");
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
  addEventListener("pagehide", () => es.close());
  addEventListener("pageshow", (e) => {
    if (e.persisted) location.reload();
  });
  connect();
})();
