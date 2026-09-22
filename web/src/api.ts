const TOKEN_KEY = "providerapi.adminToken";

export function getAdminToken(): string {
  return sessionStorage.getItem(TOKEN_KEY) ?? "";
}

export function setAdminToken(token: string): void {
  if (token) {
    sessionStorage.setItem(TOKEN_KEY, token);
  } else {
    sessionStorage.removeItem(TOKEN_KEY);
  }
}

export class ApiError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

async function apiFetch<T>(path: string): Promise<T> {
  const headers: Record<string, string> = {};
  const token = getAdminToken();
  if (token) {
    headers["X-Admin-Token"] = token;
  }
  const res = await fetch(path, { headers });
  if (!res.ok) {
    const text = await res.text();
    throw new ApiError(res.status, text || res.statusText);
  }
  return res.json() as Promise<T>;
}

export interface RequestRow {
  id: string;
  client_request_id: string;
  started_at: number;
  finished_at: number;
  client_model: string;
  provider: string;
  upstream_model: string;
  reasoning: string;
  status: string;
  http_status: number;
  input_tokens: number;
  output_tokens: number;
  ttft_ms: number;
  duration_ms: number;
  plugin_id: string;
  plugin_version: string;
  error_message?: string;
}

export interface TimelineEvent {
  seq: number;
  ts: number;
  type: string;
  payload: unknown;
}

export interface RequestDetail {
  request_id: string;
  http_status: number;
  started_at: number;
  client: { model: string; client_request_id: string };
  normalized: { reasoning: string };
  routing: {
    provider: string;
    upstream_model: string;
    plugin: string;
    plugin_version: string;
  };
  timeline: TimelineEvent[];
  usage: {
    input_tokens: number;
    output_tokens: number;
    ttft_ms: number;
    duration_ms: number;
  };
  error?: { type: string; code: string; message: string } | null;
  full?: unknown;
}

export interface AdminModel {
  id: string;
  provider: string;
  upstream_model: string;
  reasoning: string;
  context_length: number;
  context_length_source: "configured" | "fallback";
}

export interface PluginRow {
  instance_id: string;
  plugin: string;
  version: string;
  protocol_version: string;
  healthy: boolean;
  pid: number;
  capabilities: { stream: boolean; tools: boolean; reasoning: boolean };
  last_error?: string;
}

export async function fetchRequests(): Promise<RequestRow[]> {
  const body = await apiFetch<{ data: RequestRow[] }>("/admin/requests");
  return body.data ?? [];
}

export async function fetchRequestDetail(id: string): Promise<RequestDetail> {
  return apiFetch<RequestDetail>(`/admin/requests/${encodeURIComponent(id)}`);
}

export async function fetchModels(): Promise<AdminModel[]> {
  const body = await apiFetch<{ data: AdminModel[] }>("/admin/models");
  return body.data ?? [];
}

export async function fetchPlugins(): Promise<PluginRow[]> {
  const body = await apiFetch<{ data: PluginRow[] }>("/admin/plugins");
  return body.data ?? [];
}
