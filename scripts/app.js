window.App = {
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

  async fetchJson(url) {
    const res = await fetch(url);
    if (!res.ok) {
      const text = await res.text();
      throw new Error(text || `Request failed: ${res.status}`);
    }
    return res.json();
  },

  formatPct(value) {
    const n = Number(value || 0);
    return `${n.toFixed(2)}%`;
  },

  formatNum(value) {
    const n = Number(value || 0);
    return n.toFixed(2);
  }
};
