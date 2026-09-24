export interface ServerDTO {
  id: number
  name: string
  url: string
  signed_in: boolean
  username: string
  gitlab: boolean
}

export type EventType = 'servers_changed' | 'login_failed' | 'open_external'

export interface ApiEvent {
  type: EventType
  payload?: Record<string, unknown>
}
