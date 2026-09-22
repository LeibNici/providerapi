import "./styles.css";
import {
  ApiError,
  fetchPlugins,
  getAdminToken,
  setAdminToken,
} from "./api";
import { createModelsView } from "./views/models";
import { createPluginsView } from "./views/plugins";
import { createRequestDetailView } from "./views/request-detail";
import { whenDisconnected } from "./dispose";
import { createRequestsView } from "./views/requests";

const app = document.getElementById("app")!;
let needsToken = false;
let shellHealthTimer: number | undefined;

function parseRoute(): { page: "requests" | "models" | "plugins" | "detail"; id?: string } {
  const path = location.pathname.replace(/\/+$/, "") || "/";
  const m = path.match(/^\/requests\/([^/]+)$/);
  if (m) return { page: "detail", id: decodeURIComponent(m[1]) };
  if (path === "/models") return { page: "models" };
  if (path === "/plugins") return { page: "plugins" };
  return { page: "requests" };
}

function navigate(path: string) {
  history.pushState(null, "", path);
  render();
}

function renderTokenGate(): void {
  clearShellHealthTimer();
  app.replaceChildren();
  const gate = document.createElement("div");
  gate.className = "token-gate panel";
  gate.innerHTML = `<h1>Admin token</h1><p class="muted">Enter the token from <code>admin.token</code> in your config.</p>`;
  const input = document.createElement("input");
  input.type = "password";
  input.placeholder = "Admin token";
  input.autocomplete = "off";
  const btn = document.createElement("button");
  btn.className = "btn";
  btn.textContent = "Continue";
  const errEl = document.createElement("div");
  errEl.className = "error";
  btn.addEventListener("click", async () => {
    setAdminToken(input.value.trim());
    try {
      await fetchPlugins();
      needsToken = false;
      navigate("/requests");
    } catch (e) {
      if (e instanceof ApiError && e.status === 401) {
        errEl.textContent = "Invalid token";
        setAdminToken("");
      } else {
        errEl.textContent = "Could not reach admin API";
      }
    }
  });
  gate.append(input, btn, errEl);
  app.append(gate);
}

function clearShellHealthTimer(): void {
  if (shellHealthTimer !== undefined) {
    window.clearInterval(shellHealthTimer);
    shellHealthTimer = undefined;
  }
}

function renderShell(main: HTMLElement, active: string): void {
  clearShellHealthTimer();
  const shell = document.createElement("div");
  shell.className = "shell";

  const header = document.createElement("header");
  header.className = "shell-header";
  const brand = document.createElement("div");
  brand.className = "brand";
  brand.textContent = "ProviderApi";
  const nav = document.createElement("nav");
  nav.className = "nav";
  for (const [label, path] of [
    ["Requests", "/requests"],
    ["Models", "/models"],
    ["Plugins", "/plugins"],
  ]) {
    const a = document.createElement("a");
    a.href = path;
    a.textContent = label;
    if (path === active || (active === "/requests" && path === "/requests")) {
      a.classList.add("active");
    }
    a.addEventListener("click", (e) => {
      e.preventDefault();
      navigate(path);
    });
    nav.append(a);
  }
  header.append(brand, nav);

  const mainEl = document.createElement("main");
  mainEl.className = "shell-main";
  mainEl.append(main);

  const footer = document.createElement("footer");
  footer.className = "shell-footer";
  const dot = document.createElement("span");
  dot.className = "health-dot";
  const label = document.createElement("span");
  label.textContent = "Plugins";
  footer.append(dot, label);

  async function refreshHealth() {
    try {
      const plugins = await fetchPlugins();
      const allOk = plugins.length > 0 && plugins.every((p) => p.healthy);
      dot.className = "health-dot " + (allOk ? "ok" : plugins.some((p) => p.healthy) ? "" : "bad");
      if (!allOk && plugins.length) dot.classList.add("bad");
    } catch {
      dot.className = "health-dot bad";
    }
  }
  refreshHealth();
  shellHealthTimer = window.setInterval(refreshHealth, 5000);
  whenDisconnected(shell, clearShellHealthTimer);

  shell.append(header, mainEl, footer);
  app.replaceChildren(shell);
}

async function render(): Promise<void> {
  const route = parseRoute();
  if (location.pathname === "/" || location.pathname === "") {
    navigate("/requests");
    return;
  }

  if (needsToken && !getAdminToken()) {
    renderTokenGate();
    return;
  }

  try {
    await fetchPlugins();
  } catch (e) {
    if (e instanceof ApiError && e.status === 401) {
      needsToken = true;
      if (!getAdminToken()) {
        renderTokenGate();
        return;
      }
      renderTokenGate();
      return;
    }
  }

  if (route.page === "detail" && route.id) {
    renderShell(
      createRequestDetailView(route.id, () => navigate("/requests")),
      "/requests",
    );
    return;
  }
  if (route.page === "models") {
    renderShell(createModelsView(), "/models");
    return;
  }
  if (route.page === "plugins") {
    renderShell(createPluginsView(), "/plugins");
    return;
  }
  renderShell(
    createRequestsView({ onOpen: (id) => navigate(`/requests/${encodeURIComponent(id)}`) }),
    "/requests",
  );
}

window.addEventListener("popstate", () => render());
render();
