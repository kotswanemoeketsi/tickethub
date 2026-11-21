-- GotATicket initial schema.
-- All timestamps are stored in UTC. Money is stored as integer minor units (cents).

CREATE TABLE users (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  email VARCHAR(255) NOT NULL,
  password_hash VARCHAR(255) NOT NULL,
  first_name VARCHAR(100) NOT NULL,
  last_name VARCHAR(100) NOT NULL,
  phone VARCHAR(30) NOT NULL DEFAULT '',
  status ENUM('active','inactive') NOT NULL DEFAULT 'active',
  last_login_at DATETIME(3) NULL,
  password_changed_at DATETIME(3) NULL,
  created_at DATETIME(3) NOT NULL,
  updated_at DATETIME(3) NOT NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uq_users_email (email)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- Roles are either platform-wide (assigned through user_roles) or organiser-scoped
-- (assigned per organiser through organiser_members).
CREATE TABLE roles (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  slug VARCHAR(64) NOT NULL,
  name VARCHAR(100) NOT NULL,
  description VARCHAR(255) NOT NULL DEFAULT '',
  scope ENUM('platform','organiser') NOT NULL DEFAULT 'platform',
  is_system BOOLEAN NOT NULL DEFAULT FALSE,
  created_at DATETIME(3) NOT NULL,
  updated_at DATETIME(3) NOT NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uq_roles_slug (slug)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- The permission catalogue is owned by the application code and synchronised on start-up.
CREATE TABLE permissions (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  code VARCHAR(100) NOT NULL,
  module VARCHAR(50) NOT NULL,
  description VARCHAR(255) NOT NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uq_permissions_code (code)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE role_permissions (
  role_id BIGINT UNSIGNED NOT NULL,
  permission_id BIGINT UNSIGNED NOT NULL,
  PRIMARY KEY (role_id, permission_id),
  CONSTRAINT fk_role_permissions_role FOREIGN KEY (role_id) REFERENCES roles (id) ON DELETE CASCADE,
  CONSTRAINT fk_role_permissions_permission FOREIGN KEY (permission_id) REFERENCES permissions (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE user_roles (
  user_id BIGINT UNSIGNED NOT NULL,
  role_id BIGINT UNSIGNED NOT NULL,
  created_at DATETIME(3) NOT NULL,
  PRIMARY KEY (user_id, role_id),
  KEY idx_user_roles_role (role_id),
  CONSTRAINT fk_user_roles_user FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE,
  CONSTRAINT fk_user_roles_role FOREIGN KEY (role_id) REFERENCES roles (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- Refresh tokens are stored hashed. Rotation keeps a family so token reuse can be detected.
CREATE TABLE refresh_tokens (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id BIGINT UNSIGNED NOT NULL,
  token_hash CHAR(64) NOT NULL,
  family_id CHAR(32) NOT NULL,
  expires_at DATETIME(3) NOT NULL,
  revoked_at DATETIME(3) NULL,
  user_agent VARCHAR(255) NOT NULL DEFAULT '',
  ip_address VARCHAR(45) NOT NULL DEFAULT '',
  created_at DATETIME(3) NOT NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uq_refresh_tokens_hash (token_hash),
  KEY idx_refresh_tokens_user (user_id),
  KEY idx_refresh_tokens_family (family_id),
  CONSTRAINT fk_refresh_tokens_user FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE password_reset_tokens (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id BIGINT UNSIGNED NOT NULL,
  token_hash CHAR(64) NOT NULL,
  expires_at DATETIME(3) NOT NULL,
  used_at DATETIME(3) NULL,
  created_at DATETIME(3) NOT NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uq_password_reset_tokens_hash (token_hash),
  CONSTRAINT fk_password_reset_tokens_user FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- An organiser is a tenant of the marketplace: the business that sells tickets.
CREATE TABLE organisers (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  name VARCHAR(150) NOT NULL,
  slug VARCHAR(160) NOT NULL,
  email VARCHAR(255) NOT NULL,
  phone VARCHAR(30) NOT NULL DEFAULT '',
  description TEXT NOT NULL,
  status ENUM('pending','active','suspended') NOT NULL DEFAULT 'pending',
  commission_bps INT UNSIGNED NOT NULL,
  approved_at DATETIME(3) NULL,
  approved_by BIGINT UNSIGNED NULL,
  created_by BIGINT UNSIGNED NULL,
  created_at DATETIME(3) NOT NULL,
  updated_at DATETIME(3) NOT NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uq_organisers_slug (slug),
  KEY idx_organisers_status (status),
  CONSTRAINT chk_organisers_commission CHECK (commission_bps <= 10000)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE organiser_members (
  organiser_id BIGINT UNSIGNED NOT NULL,
  user_id BIGINT UNSIGNED NOT NULL,
  role_id BIGINT UNSIGNED NOT NULL,
  created_at DATETIME(3) NOT NULL,
  PRIMARY KEY (organiser_id, user_id),
  KEY idx_organiser_members_user (user_id),
  KEY idx_organiser_members_role (role_id),
  CONSTRAINT fk_organiser_members_organiser FOREIGN KEY (organiser_id) REFERENCES organisers (id) ON DELETE CASCADE,
  CONSTRAINT fk_organiser_members_user FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE,
  CONSTRAINT fk_organiser_members_role FOREIGN KEY (role_id) REFERENCES roles (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE event_categories (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  name VARCHAR(100) NOT NULL,
  slug VARCHAR(110) NOT NULL,
  description VARCHAR(255) NOT NULL DEFAULT '',
  created_at DATETIME(3) NOT NULL,
  updated_at DATETIME(3) NOT NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uq_event_categories_slug (slug)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- organiser_id NULL means the venue is shared across the platform.
CREATE TABLE venues (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  organiser_id BIGINT UNSIGNED NULL,
  name VARCHAR(150) NOT NULL,
  address_line VARCHAR(255) NOT NULL DEFAULT '',
  city VARCHAR(100) NOT NULL,
  province VARCHAR(100) NOT NULL DEFAULT '',
  country VARCHAR(100) NOT NULL DEFAULT 'South Africa',
  postal_code VARCHAR(20) NOT NULL DEFAULT '',
  capacity INT UNSIGNED NULL,
  created_at DATETIME(3) NOT NULL,
  updated_at DATETIME(3) NOT NULL,
  PRIMARY KEY (id),
  KEY idx_venues_organiser (organiser_id),
  CONSTRAINT fk_venues_organiser FOREIGN KEY (organiser_id) REFERENCES organisers (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE events (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  organiser_id BIGINT UNSIGNED NOT NULL,
  category_id BIGINT UNSIGNED NULL,
  venue_id BIGINT UNSIGNED NULL,
  title VARCHAR(200) NOT NULL,
  slug VARCHAR(220) NOT NULL,
  summary VARCHAR(300) NOT NULL DEFAULT '',
  description TEXT NOT NULL,
  image_url VARCHAR(500) NOT NULL DEFAULT '',
  starts_at DATETIME(3) NOT NULL,
  ends_at DATETIME(3) NOT NULL,
  timezone VARCHAR(64) NOT NULL,
  currency CHAR(3) NOT NULL,
  capacity INT UNSIGNED NOT NULL,
  status ENUM('draft','pending_review','published','rejected','cancelled') NOT NULL DEFAULT 'draft',
  allow_reentry BOOLEAN NOT NULL DEFAULT FALSE,
  refunds_allowed BOOLEAN NOT NULL DEFAULT TRUE,
  refund_cutoff_hours INT UNSIGNED NOT NULL DEFAULT 48,
  review_notes VARCHAR(1000) NOT NULL DEFAULT '',
  submitted_at DATETIME(3) NULL,
  reviewed_at DATETIME(3) NULL,
  reviewed_by BIGINT UNSIGNED NULL,
  published_at DATETIME(3) NULL,
  cancelled_at DATETIME(3) NULL,
  cancellation_reason VARCHAR(500) NOT NULL DEFAULT '',
  created_by BIGINT UNSIGNED NULL,
  created_at DATETIME(3) NOT NULL,
  updated_at DATETIME(3) NOT NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uq_events_slug (slug),
  KEY idx_events_status_starts (status, starts_at),
  KEY idx_events_organiser (organiser_id, starts_at),
  CONSTRAINT fk_events_organiser FOREIGN KEY (organiser_id) REFERENCES organisers (id),
  CONSTRAINT fk_events_category FOREIGN KEY (category_id) REFERENCES event_categories (id) ON DELETE SET NULL,
  CONSTRAINT fk_events_venue FOREIGN KEY (venue_id) REFERENCES venues (id) ON DELETE SET NULL,
  CONSTRAINT chk_events_dates CHECK (ends_at > starts_at),
  CONSTRAINT chk_events_capacity CHECK (capacity > 0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- quantity_sold counts issued tickets (including complimentary) that are not refunded;
-- quantity_reserved counts tickets held by pending orders. The CHECK constraint is a
-- last line of defence against overselling: application code validates first.
CREATE TABLE ticket_types (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  event_id BIGINT UNSIGNED NOT NULL,
  name VARCHAR(100) NOT NULL,
  description VARCHAR(500) NOT NULL DEFAULT '',
  category ENUM('general','vip','early_bird','student','group','complimentary','other') NOT NULL DEFAULT 'general',
  price_cents BIGINT NOT NULL,
  quantity_total INT UNSIGNED NOT NULL,
  quantity_sold INT UNSIGNED NOT NULL DEFAULT 0,
  quantity_reserved INT UNSIGNED NOT NULL DEFAULT 0,
  min_per_order INT UNSIGNED NOT NULL DEFAULT 1,
  max_per_order INT UNSIGNED NOT NULL DEFAULT 10,
  sales_start_at DATETIME(3) NULL,
  sales_end_at DATETIME(3) NULL,
  is_hidden BOOLEAN NOT NULL DEFAULT FALSE,
  sort_order INT NOT NULL DEFAULT 0,
  created_at DATETIME(3) NOT NULL,
  updated_at DATETIME(3) NOT NULL,
  PRIMARY KEY (id),
  KEY idx_ticket_types_event (event_id, sort_order),
  CONSTRAINT fk_ticket_types_event FOREIGN KEY (event_id) REFERENCES events (id) ON DELETE CASCADE,
  CONSTRAINT chk_ticket_types_price CHECK (price_cents >= 0),
  CONSTRAINT chk_ticket_types_inventory CHECK (quantity_sold + quantity_reserved <= quantity_total),
  CONSTRAINT chk_ticket_types_limits CHECK (min_per_order >= 1 AND max_per_order >= min_per_order)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- discount_value is basis points for percentage discounts (1000 = 10%) and cents for fixed discounts.
-- times_used includes orders that are still reserved; it is released when a reservation lapses.
CREATE TABLE promotions (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  event_id BIGINT UNSIGNED NOT NULL,
  code VARCHAR(40) NOT NULL,
  description VARCHAR(255) NOT NULL DEFAULT '',
  discount_type ENUM('percentage','fixed') NOT NULL,
  discount_value BIGINT NOT NULL,
  max_uses INT UNSIGNED NULL,
  times_used INT UNSIGNED NOT NULL DEFAULT 0,
  starts_at DATETIME(3) NULL,
  ends_at DATETIME(3) NULL,
  is_active BOOLEAN NOT NULL DEFAULT TRUE,
  created_by BIGINT UNSIGNED NULL,
  created_at DATETIME(3) NOT NULL,
  updated_at DATETIME(3) NOT NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uq_promotions_event_code (event_id, code),
  CONSTRAINT fk_promotions_event FOREIGN KEY (event_id) REFERENCES events (id) ON DELETE CASCADE,
  CONSTRAINT chk_promotions_value CHECK (discount_value > 0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- When a promotion has rows here it only applies to the listed ticket types.
CREATE TABLE promotion_ticket_types (
  promotion_id BIGINT UNSIGNED NOT NULL,
  ticket_type_id BIGINT UNSIGNED NOT NULL,
  PRIMARY KEY (promotion_id, ticket_type_id),
  CONSTRAINT fk_promotion_ticket_types_promotion FOREIGN KEY (promotion_id) REFERENCES promotions (id) ON DELETE CASCADE,
  CONSTRAINT fk_promotion_ticket_types_ticket_type FOREIGN KEY (ticket_type_id) REFERENCES ticket_types (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE orders (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  reference VARCHAR(20) NOT NULL,
  event_id BIGINT UNSIGNED NOT NULL,
  organiser_id BIGINT UNSIGNED NOT NULL,
  customer_id BIGINT UNSIGNED NULL,
  customer_name VARCHAR(200) NOT NULL,
  customer_email VARCHAR(255) NOT NULL,
  source ENUM('online','complimentary') NOT NULL DEFAULT 'online',
  status ENUM('pending','paid','expired','cancelled','refunded','partially_refunded') NOT NULL,
  payment_method ENUM('online','eft','free','complimentary') NULL,
  currency CHAR(3) NOT NULL,
  subtotal_cents BIGINT NOT NULL,
  discount_cents BIGINT NOT NULL DEFAULT 0,
  total_cents BIGINT NOT NULL,
  refunded_cents BIGINT NOT NULL DEFAULT 0,
  commission_bps INT UNSIGNED NOT NULL DEFAULT 0,
  promotion_id BIGINT UNSIGNED NULL,
  expires_at DATETIME(3) NULL,
  paid_at DATETIME(3) NULL,
  cancelled_at DATETIME(3) NULL,
  notes VARCHAR(500) NOT NULL DEFAULT '',
  created_by BIGINT UNSIGNED NULL,
  created_at DATETIME(3) NOT NULL,
  updated_at DATETIME(3) NOT NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uq_orders_reference (reference),
  KEY idx_orders_status_expires (status, expires_at),
  KEY idx_orders_customer (customer_id, created_at),
  KEY idx_orders_event_status (event_id, status),
  KEY idx_orders_organiser_paid (organiser_id, paid_at),
  CONSTRAINT fk_orders_event FOREIGN KEY (event_id) REFERENCES events (id),
  CONSTRAINT fk_orders_organiser FOREIGN KEY (organiser_id) REFERENCES organisers (id),
  CONSTRAINT fk_orders_customer FOREIGN KEY (customer_id) REFERENCES users (id) ON DELETE SET NULL,
  CONSTRAINT fk_orders_promotion FOREIGN KEY (promotion_id) REFERENCES promotions (id) ON DELETE SET NULL,
  CONSTRAINT chk_orders_amounts CHECK (total_cents >= 0 AND discount_cents >= 0 AND refunded_cents >= 0 AND refunded_cents <= total_cents)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- Price and name are snapshotted so later ticket-type edits never change historical orders.
CREATE TABLE order_items (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  order_id BIGINT UNSIGNED NOT NULL,
  ticket_type_id BIGINT UNSIGNED NOT NULL,
  ticket_type_name VARCHAR(100) NOT NULL,
  unit_price_cents BIGINT NOT NULL,
  quantity INT UNSIGNED NOT NULL,
  discount_cents BIGINT NOT NULL DEFAULT 0,
  total_cents BIGINT NOT NULL,
  created_at DATETIME(3) NOT NULL,
  PRIMARY KEY (id),
  KEY idx_order_items_order (order_id),
  KEY idx_order_items_ticket_type (ticket_type_id),
  CONSTRAINT fk_order_items_order FOREIGN KEY (order_id) REFERENCES orders (id) ON DELETE CASCADE,
  CONSTRAINT fk_order_items_ticket_type FOREIGN KEY (ticket_type_id) REFERENCES ticket_types (id),
  CONSTRAINT chk_order_items_quantity CHECK (quantity > 0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- One attendee per ticket unit, captured during checkout.
CREATE TABLE attendees (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  order_id BIGINT UNSIGNED NOT NULL,
  order_item_id BIGINT UNSIGNED NOT NULL,
  first_name VARCHAR(100) NOT NULL,
  last_name VARCHAR(100) NOT NULL,
  email VARCHAR(255) NOT NULL,
  phone VARCHAR(30) NOT NULL DEFAULT '',
  created_at DATETIME(3) NOT NULL,
  updated_at DATETIME(3) NOT NULL,
  PRIMARY KEY (id),
  KEY idx_attendees_order (order_id),
  KEY idx_attendees_email (email),
  CONSTRAINT fk_attendees_order FOREIGN KEY (order_id) REFERENCES orders (id) ON DELETE CASCADE,
  CONSTRAINT fk_attendees_order_item FOREIGN KEY (order_item_id) REFERENCES order_items (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE payments (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  order_id BIGINT UNSIGNED NOT NULL,
  provider VARCHAR(30) NOT NULL,
  method ENUM('online','eft','free','complimentary') NOT NULL,
  status ENUM('pending','succeeded','failed','cancelled') NOT NULL,
  amount_cents BIGINT NOT NULL,
  currency CHAR(3) NOT NULL,
  provider_reference VARCHAR(100) NOT NULL,
  provider_transaction_id VARCHAR(100) NOT NULL DEFAULT '',
  checkout_url VARCHAR(1000) NOT NULL DEFAULT '',
  refunded_cents BIGINT NOT NULL DEFAULT 0,
  failure_reason VARCHAR(500) NOT NULL DEFAULT '',
  confirmed_by BIGINT UNSIGNED NULL,
  confirmation_note VARCHAR(500) NOT NULL DEFAULT '',
  paid_at DATETIME(3) NULL,
  raw_response JSON NULL,
  created_at DATETIME(3) NOT NULL,
  updated_at DATETIME(3) NOT NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uq_payments_provider_reference (provider, provider_reference),
  KEY idx_payments_order (order_id),
  KEY idx_payments_status_created (status, created_at),
  CONSTRAINT fk_payments_order FOREIGN KEY (order_id) REFERENCES orders (id),
  CONSTRAINT chk_payments_amounts CHECK (amount_cents >= 0 AND refunded_cents >= 0 AND refunded_cents <= amount_cents)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- Every webhook delivery is recorded for audit and replay protection.
CREATE TABLE webhook_events (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  provider VARCHAR(30) NOT NULL,
  event_key CHAR(64) NOT NULL,
  event_type VARCHAR(100) NOT NULL DEFAULT '',
  payload JSON NOT NULL,
  processed_at DATETIME(3) NULL,
  error VARCHAR(1000) NOT NULL DEFAULT '',
  received_at DATETIME(3) NOT NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uq_webhook_events_key (provider, event_key)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- QR codes are not stored: they are HMAC-signed from the ticket code and qr_version,
-- so a database leak alone cannot be used to forge tickets.
CREATE TABLE tickets (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  code VARCHAR(16) NOT NULL,
  order_id BIGINT UNSIGNED NOT NULL,
  order_item_id BIGINT UNSIGNED NOT NULL,
  event_id BIGINT UNSIGNED NOT NULL,
  ticket_type_id BIGINT UNSIGNED NOT NULL,
  attendee_id BIGINT UNSIGNED NOT NULL,
  price_cents BIGINT NOT NULL,
  status ENUM('valid','void','refunded') NOT NULL DEFAULT 'valid',
  qr_version INT UNSIGNED NOT NULL DEFAULT 1,
  checkin_state ENUM('not_checked_in','checked_in','checked_out') NOT NULL DEFAULT 'not_checked_in',
  first_checked_in_at DATETIME(3) NULL,
  last_scanned_at DATETIME(3) NULL,
  is_complimentary BOOLEAN NOT NULL DEFAULT FALSE,
  created_at DATETIME(3) NOT NULL,
  updated_at DATETIME(3) NOT NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uq_tickets_code (code),
  UNIQUE KEY uq_tickets_attendee (attendee_id),
  KEY idx_tickets_order (order_id),
  KEY idx_tickets_event_state (event_id, checkin_state),
  CONSTRAINT fk_tickets_order FOREIGN KEY (order_id) REFERENCES orders (id),
  CONSTRAINT fk_tickets_order_item FOREIGN KEY (order_item_id) REFERENCES order_items (id),
  CONSTRAINT fk_tickets_event FOREIGN KEY (event_id) REFERENCES events (id),
  CONSTRAINT fk_tickets_ticket_type FOREIGN KEY (ticket_type_id) REFERENCES ticket_types (id),
  CONSTRAINT fk_tickets_attendee FOREIGN KEY (attendee_id) REFERENCES attendees (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- Every scan is recorded, including rejected ones, to give a full attendance audit trail.
CREATE TABLE check_ins (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  event_id BIGINT UNSIGNED NOT NULL,
  ticket_id BIGINT UNSIGNED NULL,
  direction ENUM('in','out') NOT NULL,
  result ENUM('accepted','rejected') NOT NULL,
  reason VARCHAR(50) NOT NULL DEFAULT '',
  gate VARCHAR(50) NOT NULL DEFAULT '',
  scanned_by BIGINT UNSIGNED NOT NULL,
  scanned_at DATETIME(3) NOT NULL,
  PRIMARY KEY (id),
  KEY idx_check_ins_event_time (event_id, scanned_at),
  KEY idx_check_ins_ticket (ticket_id),
  CONSTRAINT fk_check_ins_event FOREIGN KEY (event_id) REFERENCES events (id),
  CONSTRAINT fk_check_ins_ticket FOREIGN KEY (ticket_id) REFERENCES tickets (id),
  CONSTRAINT fk_check_ins_scanned_by FOREIGN KEY (scanned_by) REFERENCES users (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE refunds (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  reference VARCHAR(24) NOT NULL,
  order_id BIGINT UNSIGNED NOT NULL,
  payment_id BIGINT UNSIGNED NULL,
  amount_cents BIGINT NOT NULL,
  currency CHAR(3) NOT NULL,
  reason VARCHAR(500) NOT NULL,
  status ENUM('requested','approved','rejected','processing','completed','failed') NOT NULL,
  initiated_by ENUM('customer','staff','system') NOT NULL,
  requested_by BIGINT UNSIGNED NULL,
  reviewed_by BIGINT UNSIGNED NULL,
  reviewed_at DATETIME(3) NULL,
  review_note VARCHAR(500) NOT NULL DEFAULT '',
  processed_by BIGINT UNSIGNED NULL,
  processed_at DATETIME(3) NULL,
  provider_refund_id VARCHAR(100) NOT NULL DEFAULT '',
  bank_reference VARCHAR(100) NOT NULL DEFAULT '',
  failure_reason VARCHAR(500) NOT NULL DEFAULT '',
  created_at DATETIME(3) NOT NULL,
  updated_at DATETIME(3) NOT NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uq_refunds_reference (reference),
  KEY idx_refunds_order (order_id),
  KEY idx_refunds_status (status, created_at),
  CONSTRAINT fk_refunds_order FOREIGN KEY (order_id) REFERENCES orders (id),
  CONSTRAINT fk_refunds_payment FOREIGN KEY (payment_id) REFERENCES payments (id),
  CONSTRAINT chk_refunds_amount CHECK (amount_cents >= 0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE refund_items (
  refund_id BIGINT UNSIGNED NOT NULL,
  ticket_id BIGINT UNSIGNED NOT NULL,
  amount_cents BIGINT NOT NULL,
  PRIMARY KEY (refund_id, ticket_id),
  KEY idx_refund_items_ticket (ticket_id),
  CONSTRAINT fk_refund_items_refund FOREIGN KEY (refund_id) REFERENCES refunds (id) ON DELETE CASCADE,
  CONSTRAINT fk_refund_items_ticket FOREIGN KEY (ticket_id) REFERENCES tickets (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE audit_logs (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  actor_id BIGINT UNSIGNED NULL,
  actor_email VARCHAR(255) NOT NULL DEFAULT '',
  action VARCHAR(100) NOT NULL,
  entity_type VARCHAR(50) NOT NULL,
  entity_id BIGINT UNSIGNED NULL,
  organiser_id BIGINT UNSIGNED NULL,
  summary VARCHAR(500) NOT NULL DEFAULT '',
  changes JSON NULL,
  ip_address VARCHAR(45) NOT NULL DEFAULT '',
  user_agent VARCHAR(255) NOT NULL DEFAULT '',
  created_at DATETIME(3) NOT NULL,
  PRIMARY KEY (id),
  KEY idx_audit_logs_created (created_at),
  KEY idx_audit_logs_entity (entity_type, entity_id),
  KEY idx_audit_logs_actor (actor_id, created_at),
  KEY idx_audit_logs_organiser (organiser_id, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- Transactional outbox / job queue processed by the background worker.
CREATE TABLE jobs (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  type VARCHAR(100) NOT NULL,
  payload JSON NOT NULL,
  status ENUM('pending','running','completed','failed') NOT NULL DEFAULT 'pending',
  attempts INT UNSIGNED NOT NULL DEFAULT 0,
  max_attempts INT UNSIGNED NOT NULL DEFAULT 8,
  run_at DATETIME(3) NOT NULL,
  locked_at DATETIME(3) NULL,
  locked_by VARCHAR(100) NOT NULL DEFAULT '',
  last_error TEXT NULL,
  created_at DATETIME(3) NOT NULL,
  updated_at DATETIME(3) NOT NULL,
  PRIMARY KEY (id),
  KEY idx_jobs_status_run_at (status, run_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
