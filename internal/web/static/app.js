"use strict";
(() => {
  const COLS = ["game", "system", "kind", "oses", "url", "extra"];
  const REFRESH_MS = 60_000;

  const $ = (sel) => document.querySelector(sel);
  const tbody = $("#rows");
  const countEl = $("#count");
  const emptyEl = $("#empty");
  const clearBtn = $("#clear");
  const globalInput = $("#q");
  const filterEls = Object.fromEntries(
    [...document.querySelectorAll("[data-filter]")].map((el) => [el.dataset.filter, el]),
  );
  const sortBtns = [...document.querySelectorAll("[data-sort]")];

  let rows = [];
  let sort = { col: "game", dir: 1 };
  let lastJSON = "";

  // Case- and accent-insensitive: "pokemon" matches "Pokémon".
  const norm = (s) => s.normalize("NFD").replace(/\p{M}/gu, "").toLowerCase();
  const cellText = (r, col) => (col === "oses" ? r.oses.join(", ") : r[col]);

  function prepare(data) {
    return data.map((r) => {
      r.oses = r.oses || [];
      r.extra = r.extra || "";
      r._n = Object.fromEntries(COLS.map((c) => [c, norm(cellText(r, c))]));
      r._all = COLS.map((c) => r._n[c]).join(" \u0000 ");
      return r;
    });
  }

  function readState() {
    const p = new URLSearchParams(location.search);
    globalInput.value = p.get("q") || "";
    for (const c of COLS) if (filterEls[c]) filterEls[c].value = p.get(c) || "";
    const s = p.get("sort");
    if (s) {
      const col = s.replace(/^-/, "");
      if (COLS.includes(col)) sort = { col, dir: s.startsWith("-") ? -1 : 1 };
    }
  }

  function writeState() {
    const p = new URLSearchParams();
    if (globalInput.value) p.set("q", globalInput.value);
    for (const c of COLS) if (filterEls[c]?.value) p.set(c, filterEls[c].value);
    if (sort.col !== "game" || sort.dir !== 1) p.set("sort", (sort.dir < 0 ? "-" : "") + sort.col);
    const qs = p.toString();
    history.replaceState(null, "", qs ? "?" + qs : location.pathname);
  }

  function render() {
    // Global search: every word must appear somewhere in the row.
    const words = norm(globalInput.value).split(/\s+/).filter(Boolean);
    const filters = COLS.map((c) => [c, norm(filterEls[c]?.value || "").trim()]).filter(([, v]) => v);

    const out = rows.filter((r) => {
      for (const [c, v] of filters) {
        if (c === "kind" ? r.kind !== v : !r._n[c].includes(v)) return false;
      }
      return words.every((w) => r._all.includes(w));
    });

    out.sort((a, b) => {
      const d = a._n[sort.col].localeCompare(b._n[sort.col]);
      return d * sort.dir || a._n.game.localeCompare(b._n.game);
    });

    const frag = document.createDocumentFragment();
    for (const r of out) frag.append(renderRow(r, words));
    tbody.replaceChildren(frag);

    const active = words.length > 0 || filters.length > 0;
    countEl.textContent = active
      ? `Showing ${out.length} of ${rows.length} ${rows.length === 1 ? "entry" : "entries"}`
      : `${rows.length} ${rows.length === 1 ? "entry" : "entries"}`;
    emptyEl.hidden = out.length > 0;
    if (!rows.length) emptyEl.textContent = "The list is empty so far.";
    else emptyEl.textContent = "Nothing matches these filters.";
    clearBtn.hidden = !active;

    for (const b of sortBtns) {
      if (b.dataset.sort === sort.col) b.setAttribute("aria-sort", sort.dir > 0 ? "ascending" : "descending");
      else b.removeAttribute("aria-sort");
    }
  }

  // Appends text to el, wrapping global-search matches in <mark>. Never uses innerHTML.
  function highlight(el, text, words) {
    if (!words.length) { el.append(text); return; }
    const n = norm(text);
    // norm() keeps string length for most text, but bail out safely if it doesn't.
    if (n.length !== text.length) { el.append(text); return; }
    const marks = new Array(text.length).fill(false);
    for (const w of words) {
      for (let i = n.indexOf(w); i !== -1; i = n.indexOf(w, i + 1)) marks.fill(true, i, i + w.length);
    }
    let i = 0;
    while (i < text.length) {
      let j = i;
      while (j < text.length && marks[j] === marks[i]) j++;
      const part = text.slice(i, j);
      if (marks[i]) { const m = document.createElement("mark"); m.textContent = part; el.append(m); }
      else el.append(part);
      i = j;
    }
  }

  function td(cls) { const el = document.createElement("td"); el.className = cls; return el; }

  function renderRow(r, words) {
    const tr = document.createElement("tr");

    const game = td("game"); highlight(game, r.game, words);
    const system = td("system"); highlight(system, r.system, words);

    const kind = td("kind");
    const tag = document.createElement("span");
    tag.className = "tag " + (r.kind === "decomp" ? "decomp" : "recomp");
    highlight(tag, r.kind, words);
    kind.append(tag);

    const os = td("oses");
    for (const name of r.oses) {
      const chip = document.createElement("span");
      chip.className = "os";
      highlight(chip, name, words);
      os.append(chip);
    }

    const url = td("url");
    if (/^https?:\/\//i.test(r.url)) {
      const a = document.createElement("a");
      a.href = r.url; a.rel = "noopener noreferrer"; a.target = "_blank";
      highlight(a, r.url.replace(/^https?:\/\//i, ""), words);
      url.append(a);
    } else url.append(r.url);

    const extra = td("extra");
    renderExtra(extra, r.extra, words);

    tr.append(game, system, kind, os, url, extra);
    return tr;
  }

  // Free text, line breaks kept; http(s) links become clickable. Never uses innerHTML.
  const LINK_RE = /https?:\/\/[^\s<>"']+[^\s<>"'.,;:!?)\]]/gi;
  function renderExtra(el, text, words) {
    let last = 0;
    for (const m of text.matchAll(LINK_RE)) {
      highlight(el, text.slice(last, m.index), words);
      const a = document.createElement("a");
      a.href = m[0]; a.rel = "noopener noreferrer"; a.target = "_blank";
      highlight(a, m[0], words);
      el.append(a);
      last = m.index + m[0].length;
    }
    highlight(el, text.slice(last), words);
  }

  function fillDatalist(id, values) {
    const dl = document.getElementById(id);
    const opts = [...new Set(values)].sort((a, b) => a.localeCompare(b)).map((v) => {
      const o = document.createElement("option"); o.value = v; return o;
    });
    dl.replaceChildren(...opts);
  }

  async function load() {
    try {
      const res = await fetch("api/recomps", { cache: "no-store" });
      if (!res.ok) throw new Error(res.status + " " + res.statusText);
      const text = await res.text();
      if (text === lastJSON) return; // nothing changed since last poll
      lastJSON = text;
      rows = prepare(JSON.parse(text));
      fillDatalist("dl-game", rows.map((r) => r.game));
      fillDatalist("dl-system", rows.map((r) => r.system));
      fillDatalist("dl-oses", rows.flatMap((r) => r.oses));
      render();
    } catch (err) {
      if (!rows.length) countEl.textContent = "Couldn't load the list (" + err.message + ").";
    }
  }

  let debounce;
  const onInput = () => {
    clearTimeout(debounce);
    debounce = setTimeout(() => { render(); writeState(); }, 80);
  };
  globalInput.addEventListener("input", onInput);
  for (const el of Object.values(filterEls)) el.addEventListener(el.tagName === "SELECT" ? "change" : "input", onInput);

  for (const b of sortBtns) {
    b.addEventListener("click", () => {
      const col = b.dataset.sort;
      sort = sort.col === col ? { col, dir: -sort.dir } : { col, dir: 1 };
      render(); writeState();
    });
  }

  clearBtn.addEventListener("click", () => {
    globalInput.value = "";
    for (const el of Object.values(filterEls)) el.value = "";
    render(); writeState();
    globalInput.focus();
  });

  // "/" focuses the global search, like many sites.
  document.addEventListener("keydown", (e) => {
    if (e.key === "/" && !/^(INPUT|SELECT|TEXTAREA)$/.test(document.activeElement?.tagName)) {
      e.preventDefault(); globalInput.focus();
    }
  });

  // Keep the page in sync with the database.
  setInterval(() => { if (!document.hidden) load(); }, REFRESH_MS);
  document.addEventListener("visibilitychange", () => { if (!document.hidden) load(); });

  readState();
  load();
})();
