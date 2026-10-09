(function () {
  "use strict";

  var failures = {
    "htmx:timeout": "Сервер не ответил. Попробуйте ещё раз",
    "htmx:sendError": "Нет связи. Попробуйте ещё раз",
    "htmx:responseError": "Ошибка сервера. Попробуйте ещё раз",
  };

  var toastTimer;
  function toast(text) {
    var el = document.getElementById("toast");
    if (!el) return;
    el.textContent = text;
    el.hidden = false;
    clearTimeout(toastTimer);
    toastTimer = setTimeout(function () { el.hidden = true; }, 4000);
  }

  // A failed check-in: the browser already flipped the checkbox on click,
  // so put both boxes back to the state the server last rendered.
  function onFailure(elt, text) {
    var state = elt && elt.closest ? elt.closest(".state") : null;
    if (state) {
      state.querySelectorAll("input[type=checkbox]").forEach(function (cb) {
        cb.checked = cb.defaultChecked;
      });
      var msg = state.querySelector(".msg");
      if (msg) {
        msg.textContent = text;
        msg.className = "msg error";
      }
      return;
    }
    if (elt && elt.id === "overview") {
      setUpdated("нет связи с сервером", true);
      return;
    }
    toast(text);
  }

  Object.keys(failures).forEach(function (name) {
    document.addEventListener(name, function (e) {
      onFailure(e.detail.elt, failures[name]);
    });
  });

  // Door page: give the centred search bar a backdrop once it is pinned to the top.
  var bar = document.querySelector(".bar-center");
  if (bar && "IntersectionObserver" in window) {
    var sentinel = document.createElement("div");
    bar.parentNode.insertBefore(sentinel, bar);
    new IntersectionObserver(function (entries) {
      bar.classList.toggle("stuck", !entries[0].isIntersecting);
    }).observe(sentinel);
  }

  // Admin page: live filter over the polled list, and a "last updated" stamp.
  function words(s) {
    return s.toLowerCase().replace(/ё/g, "е").split(/\s+/).filter(Boolean);
  }

  function applyFilter() {
    var filter = document.getElementById("filter");
    if (!filter) return;
    var ws = words(filter.value);
    document.querySelectorAll("#overview tr[data-key]").forEach(function (tr) {
      var key = tr.getAttribute("data-key");
      tr.hidden = !ws.every(function (w) { return key.indexOf(w) !== -1; });
    });
  }

  function setUpdated(text, isError) {
    var el = document.getElementById("updated");
    if (!el) return;
    el.textContent = text;
    el.classList.toggle("error", !!isError);
  }

  document.addEventListener("input", function (e) {
    if (e.target && e.target.id === "filter") applyFilter();
  });

  var syncMsgTimer;
  document.addEventListener("htmx:afterSwap", function (e) {
    if (e.detail.target && e.detail.target.id === "sync-msg") {
      clearTimeout(syncMsgTimer);
      syncMsgTimer = setTimeout(function () { e.detail.target.textContent = ""; }, 8000);
    }
    if (e.detail.target && e.detail.target.id === "overview") {
      applyFilter();
      setUpdated("обновлено " + new Date().toLocaleTimeString("ru-RU"), false);
    }
  });
})();
