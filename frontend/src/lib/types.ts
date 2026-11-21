// Types mirroring the JSON returned by the Go API.

export interface PageMeta {
  page: number
  per_page: number
  total: number
}

export interface Paginated<T> {
  data: T[]
  meta: PageMeta
}

export interface Profile {
  id: number
  email: string
  first_name: string
  last_name: string
  phone: string
}

export interface OrganiserAccess {
  id: number
  name: string
  slug: string
  status: string
  role_name: string
  permissions: string[]
}

export interface Me {
  user: Profile
  is_super_admin: boolean
  all_organisers: boolean
  staff_access: boolean
  permissions: string[]
  organisers: OrganiserAccess[]
}

export interface Session {
  access_token: string
  access_token_expires_at: string
  user: Profile
}

export interface Category {
  id: number
  name: string
  slug: string
  description: string
  event_count: number
}

export interface Venue {
  id: number
  organiser_id: number | null
  name: string
  address_line: string
  city: string
  province: string
  country: string
  postal_code: string
  latitude: number | null
  longitude: number | null
  capacity: number | null
}

export interface VenueSummary {
  id: number
  name: string
  address_line: string
  city: string
  province: string
  country: string
}

export interface PublicEvent {
  id: number
  slug: string
  title: string
  summary: string
  image_url: string
  starts_at: string
  ends_at: string
  timezone: string
  currency: string
  organiser_name: string
  category_name: string
  category_slug: string
  venue_name: string
  venue_city: string
  from_price_cents: number | null
  sold_out: boolean
  selling_fast: boolean
  phase: string
  /** Set when events are found by distance from the buyer. */
  distance_km?: number
}

export interface PublicTicketType {
  id: number
  name: string
  description: string
  category: string
  price_cents: number
  available: number
  min_per_order: number
  max_per_order: number
  sale_status: string
  sales_start_at: string | null
  sales_end_at: string | null
}

export interface PublicEventDetail extends PublicEvent {
  description: string
  venue: VenueSummary | null
  refunds_allowed: boolean
  refund_cutoff_hours: number
  ticket_types: PublicTicketType[]
}

export interface EventStats {
  tickets_sold: number
  tickets_reserved: number
  tickets_available: number
  checked_in: number
  net_revenue_cents: number
  orders_paid: number
  ticket_types: number
}

export interface AdminEvent {
  id: number
  organiser_id: number
  organiser_name: string
  category_id: number | null
  category_name: string
  venue: VenueSummary | null
  title: string
  slug: string
  summary: string
  description: string
  image_url: string
  starts_at: string
  ends_at: string
  starts_at_local: string
  ends_at_local: string
  timezone: string
  currency: string
  capacity: number
  status: string
  phase: string
  allow_reentry: boolean
  refunds_allowed: boolean
  refund_cutoff_hours: number
  review_notes: string
  submitted_at: string | null
  published_at: string | null
  cancelled_at: string | null
  cancellation_reason: string
  created_at: string
  stats: EventStats
}

export interface TicketType {
  id: number
  event_id: number
  name: string
  description: string
  category: string
  price_cents: number
  quantity_total: number
  quantity_sold: number
  quantity_reserved: number
  quantity_available: number
  min_per_order: number
  max_per_order: number
  sales_start_at: string | null
  sales_end_at: string | null
  sales_start_at_local: string
  sales_end_at_local: string
  is_hidden: boolean
  sort_order: number
  sale_status: string
}

export interface Promotion {
  id: number
  event_id: number
  code: string
  description: string
  discount_type: 'percentage' | 'fixed'
  discount_value: number
  max_uses: number | null
  times_used: number
  starts_at_local: string
  ends_at_local: string
  is_active: boolean
  ticket_type_ids: number[]
}

export interface EventRef {
  id: number
  title: string
  slug: string
  status: string
  starts_at: string
  ends_at: string
  timezone: string
  image_url: string
  venue_name: string
  venue_address: string
  venue_city: string
  organiser_id: number
  organiser_name: string
  refunds_allowed: boolean
  refund_cutoff_hours: number
}

export interface OrderItem {
  id: number
  ticket_type_id: number
  ticket_type_name: string
  unit_price_cents: number
  quantity: number
  discount_cents: number
  total_cents: number
}

export interface Attendee {
  id: number
  order_item_id: number
  first_name: string
  last_name: string
  email: string
  phone: string
}

export interface Ticket {
  id: number
  code: string
  display_code: string
  order_id: number
  order_reference: string
  ticket_type_id: number
  ticket_type_name: string
  attendee_id: number
  holder_first_name: string
  holder_last_name: string
  holder_email: string
  price_cents: number
  currency: string
  status: string
  checkin_state: string
  first_checked_in_at: string | null
  is_complimentary: boolean
  qr_payload?: string
  event?: EventRef
}

export interface Payment {
  id: number
  order_id: number
  order_reference: string
  provider: string
  method: string
  status: string
  amount_cents: number
  refunded_cents: number
  currency: string
  provider_reference: string
  transaction_id: string
  failure_reason: string
  confirmation_note: string
  paid_at: string | null
  created_at: string
  customer_name: string
  customer_email: string
  event_title: string
  order_expires_at: string | null
  order_status: string
}

export interface Refund {
  id: number
  reference: string
  order_id: number
  order_reference: string
  payment_method: string
  amount_cents: number
  currency: string
  reason: string
  status: string
  initiated_by: string
  review_note: string
  reviewed_at: string | null
  processed_at: string | null
  provider_refund_id: string
  bank_reference: string
  failure_reason: string
  created_at: string
  ticket_ids: number[]
  customer_name: string
  customer_email: string
  event_title: string
}

export interface EFTInstructions {
  bank_name: string
  account_name: string
  account_number: string
  branch_code: string
  reference: string
  amount_cents: number
  currency: string
  pay_by: string
}

export interface Order {
  id: number
  reference: string
  status: string
  source: string
  payment_method: string | null
  event: EventRef
  customer_id: number | null
  customer_name: string
  customer_email: string
  currency: string
  subtotal_cents: number
  discount_cents: number
  total_cents: number
  refunded_cents: number
  promotion_code: string
  expires_at: string | null
  paid_at: string | null
  cancelled_at: string | null
  created_at: string
  notes: string
  items: OrderItem[]
  attendees: Attendee[]
  tickets: Ticket[]
  payments: Payment[]
  refunds: Refund[]
  eft?: EFTInstructions
  can_request_refund: boolean
}

export interface OrderSummary {
  id: number
  reference: string
  status: string
  source: string
  payment_method: string | null
  event_id: number
  event_title: string
  event_starts_at: string
  event_timezone: string
  customer_name: string
  customer_email: string
  currency: string
  total_cents: number
  refunded_cents: number
  ticket_count: number
  expires_at: string | null
  paid_at: string | null
  created_at: string
}

export interface CheckoutResult {
  order: Order
  next: 'complete' | 'redirect' | 'eft'
  redirect_url?: string
  eft?: EFTInstructions
}

export interface PaymentOptions {
  online_provider: string
  eft_enabled: boolean
  reservation_minutes: number
}

export interface AttendeeRow {
  ticket_id: number
  code: string
  display_code: string
  ticket_status: string
  checkin_state: string
  first_checked_in_at: string | null
  first_name: string
  last_name: string
  email: string
  phone: string
  ticket_type_name: string
  order_id: number
  order_reference: string
  is_complimentary: boolean
}

export interface CheckinStats {
  tickets_issued: number
  checked_in: number
  currently_inside: number
  rejected_scans: number
}

export interface ScanResult {
  result: 'accepted' | 'rejected'
  reason?: string
  message: string
  direction: 'in' | 'out'
  ticket?: {
    id: number
    display_code: string
    holder_name: string
    ticket_type_name: string
    order_reference: string
    checkin_state: string
    first_checked_in_at: string | null
    is_complimentary: boolean
  }
  stats: CheckinStats
  scanned_at: string
}

export interface ScanRecord {
  id: number
  direction: string
  result: string
  reason: string
  gate: string
  scanned_at: string
  scanned_by: string
  display_code: string
  holder_name: string
  ticket_type: string
}

export interface ScannableEvent {
  id: number
  title: string
  starts_at: string
  ends_at: string
  timezone: string
  venue: string
}

export interface TrendPoint {
  date: string
  revenue_cents: number
  tickets: number
}

export interface DashboardSummary {
  currency: string
  net_revenue_cents: number
  revenue_last_30_days_cents: number
  tickets_sold: number
  orders_paid: number
  upcoming_events: number
  live_events: number
  completed_events: number
  pending_review: number
  draft_events: number
  pending_eft_payments: number
  pending_refunds: number
  inventory: { capacity: number; sold: number; reserved: number; available: number }
  attendance: { tickets_issued: number; checked_in: number }
  sales_trend: TrendPoint[]
  recent_transactions: {
    order_id: number
    order_reference: string
    customer_name: string
    event_title: string
    method: string
    amount_cents: number
    paid_at: string
  }[]
  upcoming_list: { id: number; title: string; starts_at: string; timezone: string; capacity: number; sold: number }[]
  /** Set when the dashboard is narrowed to one event. */
  event?: { id: number; title: string; status: string; starts_at: string; ends_at: string; timezone: string; capacity: number }
}

export interface ReportDefinition {
  name: string
  title: string
  description: string
  permission: string
  needs_event: boolean
  event_filter: 'required' | 'optional' | 'none'
}

export interface ReportColumn {
  key: string
  label: string
  kind: 'text' | 'int' | 'money' | 'percent' | 'datetime' | 'date'
}

export interface ReportTable {
  name: string
  title: string
  description: string
  currency: string
  columns: ReportColumn[]
  rows: unknown[][]
  totals?: unknown[]
  generated_at: string
}

export interface Organiser {
  id: number
  name: string
  slug: string
  email: string
  phone: string
  description: string
  status: string
  commission_bps: number
  approved_at: string | null
  created_at: string
  member_count: number
  event_count: number
}

export interface Member {
  user_id: number
  email: string
  first_name: string
  last_name: string
  user_status: string
  role_id: number
  role_name: string
  joined_at: string
}

export interface RoleRef {
  id: number
  slug: string
  name: string
}

export interface User {
  id: number
  email: string
  first_name: string
  last_name: string
  phone: string
  status: string
  last_login_at: string | null
  created_at: string
  roles: RoleRef[]
  memberships: { organiser_id: number; organiser_name: string; organiser_status: string; role_id: number; role_name: string }[]
}

export interface Role {
  id: number
  slug: string
  name: string
  description: string
  scope: 'platform' | 'organiser'
  is_system: boolean
  permissions: string[]
  user_count: number
}

export interface Permission {
  code: string
  module: string
  description: string
  organiser: boolean
}

export interface AuditLog {
  id: number
  actor_id: number | null
  actor_email: string
  action: string
  entity_type: string
  entity_id: number | null
  organiser_id: number | null
  summary: string
  changes?: unknown
  ip_address: string
  created_at: string
}

export interface LocationCity {
  name: string
  /** Upcoming events in the city; zero for towns with nothing on yet. */
  event_count: number
  latitude: number | null
  longitude: number | null
}

export interface LocationProvince {
  name: string
  event_count: number
  cities: LocationCity[]
}
