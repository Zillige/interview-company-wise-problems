(() => {
  const CACHE_PREFIX = 'icwp:cache:v1:';
  const PROGRESS_KEY = 'icwp:progress:v1';
  const PROGRESS_STATUSES = ['Not solved', 'Solved'];
  const DEFAULT_TTL_MS = 24 * 60 * 60 * 1000; // 1 day

  const memoryCache = new Map();
  const inflight = new Map();

  const now = () => Date.now();

  function readSession(url) {
    try {
      const raw = sessionStorage.getItem(CACHE_PREFIX + url);
      if (!raw) return null;
      const entry = JSON.parse(raw);
      if (!entry || typeof entry.ts !== 'number' || !('data' in entry)) return null;
      return entry;
    } catch {
      return null;
    }
  }

  function writeSession(url, entry) {
    try {
      sessionStorage.setItem(CACHE_PREFIX + url, JSON.stringify(entry));
    } catch {
      // Ignore quota/private mode errors.
    }
  }

  function deleteSession(url) {
    try {
      sessionStorage.removeItem(CACHE_PREFIX + url);
    } catch {
      // Ignore errors.
    }
  }

  function getCached(url) {
    const inMem = memoryCache.get(url);
    if (inMem) return inMem;

    const inSession = readSession(url);
    if (inSession) {
      memoryCache.set(url, inSession);
      return inSession;
    }

    return null;
  }

  function setCached(url, data) {
    const entry = { ts: now(), data };
    memoryCache.set(url, entry);
    writeSession(url, entry);
    return data;
  }

  async function fetchNetwork(url) {
    const res = await fetch(url);
    if (!res.ok) {
      const text = await res.text();
      throw new Error(text || `Request failed: ${res.status}`);
    }
    return res.json();
  }

  async function refresh(url) {
    if (inflight.has(url)) return inflight.get(url);

    const req = (async () => {
      const data = await fetchNetwork(url);
      setCached(url, data);
      return data;
    })().finally(() => inflight.delete(url));

    inflight.set(url, req);
    return req;
  }

  function readProgressMap() {
    try {
      const raw = localStorage.getItem(PROGRESS_KEY);
      if (!raw) return {};
      const parsed = JSON.parse(raw);
      if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) return {};

      const out = {};
      for (const [key, value] of Object.entries(parsed)) {
        const id = String(key);
        const rawStatus = value?.status;
        const status = rawStatus === 'Solved' ? 'Solved' : 'Not solved';
        if (!PROGRESS_STATUSES.includes(status)) continue;
        out[id] = {
          status,
          updatedAt: Number.isFinite(value?.updatedAt) ? value.updatedAt : now()
        };
      }
      return out;
    } catch {
      return {};
    }
  }

  function writeProgressMap(progressMap) {
    try {
      localStorage.setItem(PROGRESS_KEY, JSON.stringify(progressMap));
      return true;
    } catch {
      return false;
    }
  }

  window.App = {
    statuses: PROGRESS_STATUSES,

    difficultyClass(level) {
      const v = (level || '').toLowerCase();
      if (v === 'easy') return 'easy';
      if (v === 'medium') return 'medium';
      if (v === 'hard') return 'hard';
      return 'easy';
    },

    encodeCompany(name) {
      return encodeURIComponent((name || '').trim());
    },

    async fetchJson(url, options = {}) {
      const ttlMs = Number.isFinite(options.ttlMs) ? options.ttlMs : DEFAULT_TTL_MS;
      const useCache = options.cache !== false;
      const forceRefresh = options.forceRefresh === true;
      const staleWhileRevalidate = options.staleWhileRevalidate !== false;

      if (!useCache) return fetchNetwork(url);

      const entry = forceRefresh ? null : getCached(url);
      if (entry) {
        const ageMs = now() - entry.ts;

        if (ageMs <= ttlMs) return entry.data;

        if (staleWhileRevalidate) {
          refresh(url).catch(() => {});
          return entry.data;
        }
      }

      return refresh(url);
    },

    prefetch(url, options = {}) {
      return this.fetchJson(url, { ...options, staleWhileRevalidate: true })
        .then(() => true)
        .catch(() => false);
    },

    clearCache(url) {
      if (!url) {
        memoryCache.clear();
        try {
          for (let i = sessionStorage.length - 1; i >= 0; i--) {
            const k = sessionStorage.key(i);
            if (k && k.startsWith(CACHE_PREFIX)) sessionStorage.removeItem(k);
          }
        } catch {
          // Ignore errors.
        }
        return;
      }

      memoryCache.delete(url);
      deleteSession(url);
    },

    formatPct(value) {
      const n = Number(value || 0);
      return `${n.toFixed(2)}%`;
    },

    formatNum(value) {
      const n = Number(value || 0);
      return n.toFixed(2);
    },

    getProgressMap() {
      return readProgressMap();
    },

    getProblemStatus(problemId) {
      const id = String(problemId);
      const map = readProgressMap();
      return map[id]?.status || 'Not solved';
    },

    setProblemStatus(problemId, status) {
      const id = String(problemId);
      if (!PROGRESS_STATUSES.includes(status)) return false;

      const map = readProgressMap();
      if (status === 'Not solved') {
        delete map[id];
      } else {
        map[id] = { status, updatedAt: now() };
      }
      return writeProgressMap(map);
    },

    computeSolvedStats(problems = []) {
      const map = readProgressMap();
      const total = Array.isArray(problems) ? problems.length : 0;
      if (!total) return { solved: 0, total: 0, pct: 0 };

      let solved = 0;
      for (const p of problems) {
        if (!p) continue;
        const id = String(p.id);
        if (map[id]?.status === 'Solved') solved++;
      }

      const pct = (solved / total) * 100;
      return { solved, total, pct };
    }
  };
})();
