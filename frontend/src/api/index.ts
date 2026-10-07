import axios from 'axios'

export const http = axios.create({ baseURL: '/api', timeout: 10000 })

export function wsUrl(path: string): string {
  const proto = location.protocol === 'https:' ? 'wss' : 'ws'
  return `${proto}://${location.host}${path}`
}

// ---------- 类型 ----------
export interface Device {
  id: number; name: string; description: string; location: string; owner: string
  tags: string[]; group_id: number | null; template_id: number | null
  online: boolean; created_at: string; updated_at: string
}
export interface Template {
  id: number; key: string; name: string; category: string; icon: string
  description: string; spec: Record<string, any>; default_connections: any[]; builtin: boolean
}
export interface Connection {
  id: number; device_id: number; kind: string; name: string
  params: Record<string, any>; credential_id: number | null; enabled: boolean
}
export interface Session {
  id: number; connection_id: number; opened_by: string; opened_at: string
  closed_at: string | null; status: string; last_error: string
}
export interface SerialPort { device: string; description: string; hwid: string; is_usb: boolean }
export interface ConnectorKind { kind: string; schema: any }
export interface Stats { devices_total: number; devices_online: number; sessions_online: number; templates_total: number }
export interface Group { id: number; name: string; parent_id: number | null }

// ---------- API ----------
export const api = {
  stats: () => http.get<Stats>('/stats').then(r => r.data),
  groups: () => http.get<Group[]>('/groups').then(r => r.data),
  createGroup: (name: string) => http.post<Group>('/groups', { name }).then(r => r.data),

  devices: (params?: any) => http.get<{ total: number; items: Device[] }>('/devices', { params }).then(r => r.data),
  device: (id: number) => http.get<Device>(`/devices/${id}`).then(r => r.data),
  createDevice: (body: Partial<Device>) => http.post<Device>('/devices', body).then(r => r.data),
  updateDevice: (id: number, body: Partial<Device>) => http.put<Device>(`/devices/${id}`, body).then(r => r.data),
  deleteDevice: (id: number) => http.delete(`/devices/${id}`),
  fromTemplate: (key: string, body: any) => http.post<Device>(`/devices/from-template/${key}`, body).then(r => r.data),

  templates: () => http.get<Template[]>('/templates').then(r => r.data),
  template: (key: string) => http.get<Template>(`/templates/${key}`).then(r => r.data),

  connections: (deviceId: number) => http.get<Connection[]>(`/devices/${deviceId}/connections`).then(r => r.data),
  createConnection: (deviceId: number, body: any) => http.post<Connection>(`/devices/${deviceId}/connections`, body).then(r => r.data),
  updateConnection: (id: number, body: any) => http.put<Connection>(`/connections/${id}`, body).then(r => r.data),
  deleteConnection: (id: number) => http.delete(`/connections/${id}`),
  openSession: (connId: number) => http.post<Session>(`/connections/${connId}/open`).then(r => r.data),
  connectorKinds: () => http.get<ConnectorKind[]>('/connector-kinds').then(r => r.data),

  serialPorts: () => http.get<SerialPort[]>('/serial/ports').then(r => r.data),

  sessions: (status?: string) => http.get<Session[]>('/sessions', { params: { status } }).then(r => r.data),
  session: (id: number) => http.get<Session>(`/sessions/${id}`).then(r => r.data),
  closeSession: (id: number) => http.post<Session>(`/sessions/${id}/close`).then(r => r.data),
  sessionLogs: (id: number) => http.get<any[]>(`/sessions/${id}/logs`).then(r => r.data),
}
