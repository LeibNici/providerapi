import { fetchModels, type AdminModel } from "../api";
import { formatContext } from "../format";

export function createModelsView(): HTMLElement {
  const root = document.createElement("div");
  const tableWrap = document.createElement("div");
  root.append(tableWrap);

  let drawerBackdrop: HTMLElement | null = null;

  function closeDrawer() {
    drawerBackdrop?.remove();
    drawerBackdrop = null;
  }

  function openDrawer(m: AdminModel) {
    closeDrawer();
    drawerBackdrop = document.createElement("div");
    drawerBackdrop.className = "drawer-backdrop";
    drawerBackdrop.addEventListener("click", (e) => {
      if (e.target === drawerBackdrop) closeDrawer();
    });
    const drawer = document.createElement("div");
    drawer.className = "drawer";
    drawer.innerHTML = `<h2 class="mono">${escapeHtml(m.id)}</h2>`;
    const dl = document.createElement("dl");
    dl.className = "detail-header";
    const fields: [string, string][] = [
      ["Provider", m.provider],
      ["Upstream", m.upstream_model],
      ["Reasoning", m.reasoning],
      ["Context", formatContext(m.context_length, m.context_length_source)],
    ];
    for (const [label, value] of fields) {
      const dt = document.createElement("dt");
      dt.textContent = label;
      const dd = document.createElement("dd");
      dd.textContent = value;
      dl.append(dt, dd);
    }
    drawer.append(dl);
    drawerBackdrop.append(drawer);
    document.body.append(drawerBackdrop);
  }

  fetchModels()
    .then((models) => {
      const sorted = [...models].sort((a, b) => a.id.localeCompare(b.id));
      const table = document.createElement("table");
      table.className = "data";
      table.innerHTML = `<thead><tr>
        <th>Alias</th><th>Provider</th><th>Upstream</th><th>Context</th>
      </tr></thead>`;
      const tbody = document.createElement("tbody");
      for (const m of sorted) {
        const tr = document.createElement("tr");
        tr.className = "clickable";
        tr.innerHTML = `
          <td class="mono">${escapeHtml(m.id)}</td>
          <td>${escapeHtml(m.provider)}</td>
          <td class="mono">${escapeHtml(m.upstream_model)}</td>
          <td class="mono">${formatContext(m.context_length, m.context_length_source)}</td>`;
        tr.addEventListener("click", () => openDrawer(m));
        tbody.append(tr);
      }
      table.append(tbody);
      tableWrap.replaceChildren(table);
    })
    .catch((err) => {
      tableWrap.textContent = err instanceof Error ? err.message : "Failed to load models";
    });

  return root;
}

function escapeHtml(s: string): string {
  return s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
}
