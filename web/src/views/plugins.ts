import { fetchPlugins, type PluginRow } from "../api";

export function createPluginsView(): HTMLElement {
  const root = document.createElement("div");
  const tableWrap = document.createElement("div");
  root.append(tableWrap);

  function capBadges(caps: PluginRow["capabilities"]): string {
    const items = [
      ["stream", caps.stream],
      ["tools", caps.tools],
      ["reasoning", caps.reasoning],
    ];
    return items
      .map(
        ([name, on]) =>
          `<span class="cap-badge ${on ? "on" : ""}">${name}</span>`,
      )
      .join("");
  }

  fetchPlugins()
    .then((plugins) => {
      const sorted = [...plugins].sort((a, b) => a.instance_id.localeCompare(b.instance_id));
      const table = document.createElement("table");
      table.className = "data";
      table.innerHTML = `<thead><tr>
        <th>Instance</th><th>Plugin</th><th>Health</th><th>PID</th><th>Capabilities</th><th>Last error</th>
      </tr></thead>`;
      const tbody = document.createElement("tbody");
      for (const p of sorted) {
        const tr = document.createElement("tr");
        const health = p.healthy
          ? '<span class="health-dot ok" title="healthy"></span>'
          : '<span class="health-dot bad" title="unhealthy"></span>';
        tr.innerHTML = `
          <td class="mono">${escapeHtml(p.instance_id)}</td>
          <td>${escapeHtml(p.plugin)} <span class="muted mono">${escapeHtml(p.version)}</span></td>
          <td>${health}</td>
          <td class="mono">${p.pid || "—"}</td>
          <td><div class="cap-badges">${capBadges(p.capabilities)}</div></td>
          <td class="mono muted">${p.last_error ? escapeHtml(p.last_error) : "—"}</td>`;
        tbody.append(tr);
      }
      table.append(tbody);
      tableWrap.replaceChildren(table);
    })
    .catch((err) => {
      tableWrap.textContent = err instanceof Error ? err.message : "Failed to load plugins";
    });

  return root;
}

function escapeHtml(s: string): string {
  return s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
}
