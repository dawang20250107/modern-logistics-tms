-- 基线 schema：从并跑期的运行库整体快照而来，是 Go 侧建库的唯一来源。
--
-- Django 退役后没有 migrations 可依赖，没有这份基线就再也建不出一个空库。
-- 已剔除 Django 运行时自带的表（migrations/content_type/auth_*/session/celery_beat/
-- token_blacklist 以及 auth 侧的两张用户 M2M）——权限与组织由 iam_* 自成一套，
-- 那批表在纯 Go 部署里没有任何引用方。
--
-- 仅在空库上执行：已有数据的库由迁移器直接记账跳过（见 migrate.go 的基线约定）。

CREATE TABLE public.accounts_user (
    password character varying(128) NOT NULL,
    last_login timestamp with time zone,
    is_superuser boolean NOT NULL,
    username character varying(150) NOT NULL,
    first_name character varying(150) NOT NULL,
    last_name character varying(150) NOT NULL,
    email character varying(254) NOT NULL,
    is_staff boolean NOT NULL,
    is_active boolean NOT NULL,
    date_joined timestamp with time zone NOT NULL,
    id uuid NOT NULL,
    phone character varying(32) NOT NULL,
    nickname character varying(64) NOT NULL,
    organization_id uuid,
    avatar character varying(100),
    preferences jsonb NOT NULL
);
CREATE TABLE public.ai_agent_suggestion (
    id uuid NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    suggestion_type character varying(64) NOT NULL,
    title character varying(160) NOT NULL,
    body text NOT NULL,
    status character varying(24) NOT NULL,
    evidence jsonb NOT NULL,
    tool_name character varying(64) NOT NULL,
    confirmed_at timestamp with time zone,
    confirmed_by_id uuid,
    waybill_id uuid
);
CREATE TABLE public.ai_agent_thread_message (
    id uuid NOT NULL,
    thread_id text NOT NULL,
    seq bigint NOT NULL,
    role text NOT NULL,
    content text DEFAULT ''::text NOT NULL,
    tool_calls jsonb,
    tool_call_id text DEFAULT ''::text NOT NULL,
    name text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);
CREATE SEQUENCE public.ai_agent_thread_message_seq_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;
ALTER SEQUENCE public.ai_agent_thread_message_seq_seq OWNED BY public.ai_agent_thread_message.seq;
CREATE TABLE public.ana_metric_snapshot (
    id uuid NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    metric_code character varying(64) NOT NULL,
    stat_date date NOT NULL,
    dimension_key character varying(64) NOT NULL,
    value numeric(18,4) NOT NULL
);
CREATE TABLE public.audit_log (
    id uuid NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    action character varying(128) NOT NULL,
    resource_type character varying(64) NOT NULL,
    resource_id character varying(64) NOT NULL,
    request_id character varying(64) NOT NULL,
    method character varying(8) NOT NULL,
    path character varying(255) NOT NULL,
    status_code integer,
    ip inet,
    payload jsonb NOT NULL,
    actor_id uuid
);
CREATE TABLE public.fin_contract (
    id uuid NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    is_deleted boolean DEFAULT false NOT NULL,
    deleted_at timestamp with time zone,
    contract_no character varying(64) NOT NULL,
    name character varying(128) DEFAULT ''::character varying NOT NULL,
    contract_type character varying(16) DEFAULT 'long_term'::character varying NOT NULL,
    party_type character varying(16) NOT NULL,
    party_id uuid NOT NULL,
    party_name character varying(128) DEFAULT ''::character varying NOT NULL,
    effective_from date NOT NULL,
    effective_to date,
    signed_at date,
    status character varying(16) DEFAULT 'active'::character varying NOT NULL,
    settlement_type character varying(16) DEFAULT 'monthly'::character varying NOT NULL,
    credit_days integer DEFAULT 30 NOT NULL,
    billing_day integer DEFAULT 1 NOT NULL,
    file_url character varying(200) DEFAULT ''::character varying NOT NULL,
    remark character varying(255) DEFAULT ''::character varying NOT NULL
);
CREATE TABLE public.fin_expense_item (
    id uuid NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    code character varying(64) NOT NULL,
    name character varying(128) NOT NULL,
    direction character varying(16) NOT NULL,
    debit_account_code character varying(64) NOT NULL,
    credit_account_code character varying(64) NOT NULL,
    is_active boolean NOT NULL
);
CREATE TABLE public.fin_expense_record (
    id uuid NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    direction character varying(16) NOT NULL,
    expense_item_code character varying(64) NOT NULL,
    amount numeric(12,2) NOT NULL,
    currency character varying(8) NOT NULL,
    occurred_at timestamp with time zone,
    risk_status character varying(32) NOT NULL,
    source_system character varying(64) NOT NULL,
    external_id character varying(64) NOT NULL,
    waybill_id uuid NOT NULL,
    payee_ref character varying(120) NOT NULL,
    payee_type character varying(16) NOT NULL,
    remark character varying(255) NOT NULL,
    calculation_detail jsonb NOT NULL,
    charge_method character varying(32) NOT NULL,
    input_snapshot jsonb NOT NULL,
    matched_condition character varying(255) NOT NULL,
    price_source character varying(32) NOT NULL,
    pricing_rule_id character varying(64) NOT NULL,
    pricing_rule_name character varying(120) NOT NULL,
    quote_id character varying(64) NOT NULL,
    rule_snapshot jsonb NOT NULL,
    contract_id uuid,
    contract_no character varying(64) DEFAULT ''::character varying NOT NULL
);
CREATE TABLE public.fin_payment_request (
    id uuid NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    request_no character varying(64) NOT NULL,
    counterparty_type character varying(32) NOT NULL,
    counterparty_ref character varying(64) NOT NULL,
    amount numeric(12,2) NOT NULL,
    reason character varying(255) NOT NULL,
    status character varying(32) NOT NULL,
    external_approval_no character varying(64) NOT NULL,
    waybill_id uuid
);
CREATE TABLE public.fin_pricing_rule (
    id uuid NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    name character varying(120) NOT NULL,
    price_type character varying(16) NOT NULL,
    expense_item_code character varying(64) NOT NULL,
    route_name character varying(160) NOT NULL,
    vehicle_type character varying(64) NOT NULL,
    base_price numeric(12,2) NOT NULL,
    min_price numeric(12,2) NOT NULL,
    priority integer NOT NULL,
    is_active boolean NOT NULL,
    carrier_id uuid,
    customer_id uuid,
    fuel_surcharge_pct numeric(6,4) NOT NULL,
    tier_prices jsonb NOT NULL,
    volumetric_factor numeric(8,4) NOT NULL,
    charge_method character varying(16) NOT NULL,
    min_charge_qty numeric(12,3) NOT NULL,
    unit_price numeric(12,2) NOT NULL,
    contract_id uuid,
    effective_from date,
    effective_to date
);
CREATE TABLE public.fin_project (
    id uuid NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    is_deleted boolean DEFAULT false NOT NULL,
    deleted_at timestamp with time zone,
    project_no character varying(64) NOT NULL,
    name character varying(128) DEFAULT ''::character varying NOT NULL,
    customer_id uuid,
    contract_id uuid,
    start_date date,
    end_date date,
    status character varying(16) DEFAULT 'active'::character varying NOT NULL,
    manager_id uuid,
    remark character varying(255) DEFAULT ''::character varying NOT NULL
);
CREATE TABLE public.fin_reimbursement (
    id uuid NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    reimb_no character varying(40) NOT NULL,
    order_no character varying(40) NOT NULL,
    category character varying(20) NOT NULL,
    amount numeric(12,2) NOT NULL,
    reason character varying(255) NOT NULL,
    status character varying(16) NOT NULL,
    approved_at timestamp with time zone,
    paid_at timestamp with time zone,
    remark character varying(255) NOT NULL,
    approved_by_id uuid,
    payment_request_id uuid,
    submitted_by_id uuid,
    waybill_id uuid
);
CREATE TABLE public.fin_statement (
    id uuid NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    statement_no character varying(40) NOT NULL,
    direction character varying(16) NOT NULL,
    counterparty_type character varying(16) NOT NULL,
    counterparty_id character varying(64) NOT NULL,
    counterparty_name character varying(160) NOT NULL,
    period_start date NOT NULL,
    period_end date NOT NULL,
    total_amount numeric(14,2) NOT NULL,
    item_count integer NOT NULL,
    external_total numeric(14,2) NOT NULL,
    status character varying(16) NOT NULL,
    confirmed_at timestamp with time zone,
    confirmed_by_id uuid,
    audited_at timestamp with time zone,
    due_date date,
    settled_amount numeric(14,2) NOT NULL,
    settled_at timestamp with time zone,
    scope_type character varying(16) DEFAULT 'all'::character varying NOT NULL,
    scope_id uuid,
    scope_name character varying(128) DEFAULT ''::character varying NOT NULL
);
CREATE TABLE public.fin_statement_line (
    id uuid NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    waybill_no character varying(40) NOT NULL,
    expense_item_code character varying(64) NOT NULL,
    amount numeric(12,2) NOT NULL,
    occurred_at timestamp with time zone,
    statement_id uuid NOT NULL,
    baseline_avg numeric(12,2),
    deviation_pct numeric(8,1),
    expense_record_id uuid,
    is_anomaly boolean NOT NULL
);
CREATE TABLE public.fin_statement_payment (
    id uuid NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    amount numeric(14,2) NOT NULL,
    method character varying(16) NOT NULL,
    paid_at date NOT NULL,
    reference_no character varying(80) NOT NULL,
    remark character varying(255) NOT NULL,
    created_by_id uuid,
    statement_id uuid NOT NULL
);
CREATE TABLE public.fin_webhook (
    id uuid NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    name character varying(120) NOT NULL,
    target_url character varying(200) NOT NULL,
    secret character varying(80) NOT NULL,
    events character varying(255) NOT NULL,
    is_active boolean NOT NULL
);
CREATE TABLE public.fin_webhook_delivery (
    id uuid NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    event_type character varying(64) NOT NULL,
    payload jsonb NOT NULL,
    status character varying(16) NOT NULL,
    response_code integer,
    attempts integer NOT NULL,
    webhook_id uuid NOT NULL
);
CREATE TABLE public.iam_account_handover (
    id uuid NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    reason character varying(255) NOT NULL,
    moved_reports integer NOT NULL,
    moved_departments integer NOT NULL,
    disabled_account boolean NOT NULL,
    operator_id uuid,
    from_employee_id uuid NOT NULL,
    to_employee_id uuid NOT NULL
);
CREATE TABLE public.iam_api_key (
    id uuid NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    name character varying(120) NOT NULL,
    key_id character varying(40) NOT NULL,
    secret character varying(80) NOT NULL,
    scopes character varying(255) NOT NULL,
    is_active boolean NOT NULL,
    last_used_at timestamp with time zone,
    organization_id uuid
);
CREATE TABLE public.iam_department (
    id uuid NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    code character varying(64) NOT NULL,
    name character varying(120) NOT NULL,
    sort_order integer NOT NULL,
    is_active boolean NOT NULL,
    organization_id uuid NOT NULL,
    parent_id uuid,
    manager_id uuid
);
CREATE TABLE public.iam_employee (
    id uuid NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    employee_no character varying(32) NOT NULL,
    name character varying(64) NOT NULL,
    phone character varying(32) NOT NULL,
    email character varying(254) NOT NULL,
    id_no character varying(32) NOT NULL,
    "position" character varying(64) NOT NULL,
    status character varying(16) NOT NULL,
    hire_date date,
    leave_date date,
    department_id uuid,
    organization_id uuid,
    supervisor_id uuid,
    user_id uuid
);
CREATE TABLE public.iam_employee_group (
    id uuid NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    code character varying(64) NOT NULL,
    name character varying(64) NOT NULL,
    description character varying(255) NOT NULL,
    is_active boolean NOT NULL
);
CREATE TABLE public.iam_employee_group_roles (
    id bigint NOT NULL,
    employeegroup_id uuid NOT NULL,
    role_id uuid NOT NULL
);
ALTER TABLE public.iam_employee_group_roles ALTER COLUMN id ADD GENERATED BY DEFAULT AS IDENTITY (
    SEQUENCE NAME public.iam_employee_group_roles_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);
CREATE TABLE public.iam_employee_groups (
    id bigint NOT NULL,
    employee_id uuid NOT NULL,
    employeegroup_id uuid NOT NULL
);
ALTER TABLE public.iam_employee_groups ALTER COLUMN id ADD GENERATED BY DEFAULT AS IDENTITY (
    SEQUENCE NAME public.iam_employee_groups_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);
CREATE TABLE public.iam_login_attempt (
    id uuid NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    username character varying(150) NOT NULL,
    success boolean NOT NULL,
    result character varying(32) NOT NULL,
    ip inet,
    user_agent character varying(255) NOT NULL,
    user_id uuid
);
CREATE TABLE public.iam_organization (
    id uuid NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    name character varying(120) NOT NULL,
    code character varying(64) NOT NULL,
    type character varying(16) NOT NULL,
    path character varying(512) NOT NULL,
    is_active boolean NOT NULL,
    parent_id uuid,
    address character varying(255) NOT NULL,
    business_phone character varying(32) NOT NULL,
    city character varying(32) NOT NULL,
    complaint_phone character varying(32) NOT NULL,
    district character varying(32) NOT NULL,
    lat numeric(9,6),
    lng numeric(9,6),
    manager_name character varying(64) NOT NULL,
    manager_phone character varying(32) NOT NULL,
    org_property character varying(16) NOT NULL,
    province character varying(32) NOT NULL,
    receipt_return_address character varying(255) NOT NULL,
    service_phone character varying(32) NOT NULL,
    short_name character varying(64) NOT NULL,
    sort_order integer NOT NULL
);
CREATE TABLE public.iam_permission (
    id uuid NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    code character varying(128) NOT NULL,
    name character varying(128) NOT NULL,
    module character varying(64) NOT NULL
);
CREATE TABLE public.iam_role (
    id uuid NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    code character varying(64) NOT NULL,
    name character varying(64) NOT NULL,
    data_scope character varying(16) NOT NULL,
    is_active boolean NOT NULL
);
CREATE TABLE public.iam_role_assignment (
    id uuid NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    organization_id uuid,
    role_id uuid NOT NULL,
    user_id uuid NOT NULL
);
CREATE TABLE public.iam_role_permissions (
    id bigint NOT NULL,
    role_id uuid NOT NULL,
    permission_id uuid NOT NULL
);
ALTER TABLE public.iam_role_permissions ALTER COLUMN id ADD GENERATED BY DEFAULT AS IDENTITY (
    SEQUENCE NAME public.iam_role_permissions_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);
CREATE TABLE public.iam_service_area (
    id uuid NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    area_type character varying(16) NOT NULL,
    province character varying(32) NOT NULL,
    city character varying(32) NOT NULL,
    district character varying(32) NOT NULL,
    region_code character varying(16) NOT NULL,
    region_name character varying(120) NOT NULL,
    priority integer NOT NULL,
    note character varying(255) NOT NULL,
    is_active boolean NOT NULL,
    organization_id uuid NOT NULL
);
CREATE TABLE public.md_b2b_partner (
    id uuid NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    is_deleted boolean NOT NULL,
    deleted_at timestamp with time zone,
    partner_type character varying(16) NOT NULL,
    code character varying(32) NOT NULL,
    name character varying(120) NOT NULL,
    contact_name character varying(64) NOT NULL,
    contact_phone character varying(32) NOT NULL,
    address character varying(255) NOT NULL,
    city character varying(64) NOT NULL,
    is_active boolean NOT NULL
);
CREATE TABLE public.md_carrier (
    id uuid NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    is_deleted boolean NOT NULL,
    deleted_at timestamp with time zone,
    code character varying(32) NOT NULL,
    name character varying(120) NOT NULL,
    contact_name character varying(64) NOT NULL,
    contact_phone character varying(32) NOT NULL,
    settlement_type character varying(32) NOT NULL,
    is_active boolean NOT NULL,
    billing_day integer NOT NULL,
    blacklist_reason character varying(255) NOT NULL,
    blacklisted boolean NOT NULL,
    business_license_no character varying(64) NOT NULL,
    credit_days integer NOT NULL,
    credit_limit numeric(14,2) NOT NULL,
    grade character varying(1) NOT NULL,
    qualification_expiry date,
    carrier_type character varying(16) NOT NULL,
    city character varying(64) NOT NULL,
    contract_expiry date,
    insurance_expiry date,
    service_area character varying(255) NOT NULL,
    tax_no character varying(64) NOT NULL,
    transport_license_no character varying(64) NOT NULL
);
CREATE TABLE public.md_carrier_lane_price (
    id uuid NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    is_deleted boolean NOT NULL,
    deleted_at timestamp with time zone,
    origin_city character varying(64) NOT NULL,
    dest_city character varying(64) NOT NULL,
    vehicle_type character varying(64) NOT NULL,
    vehicle_length_m numeric(4,1) NOT NULL,
    standard_price numeric(12,2) NOT NULL,
    min_price numeric(12,2) NOT NULL,
    max_price numeric(12,2) NOT NULL,
    last_deal_price numeric(12,2) NOT NULL,
    effective_from date,
    effective_to date,
    is_preferred boolean NOT NULL,
    is_recommended boolean NOT NULL,
    note character varying(255) NOT NULL,
    is_active boolean NOT NULL,
    carrier_id uuid NOT NULL
);
CREATE TABLE public.md_customer (
    id uuid NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    is_deleted boolean NOT NULL,
    deleted_at timestamp with time zone,
    code character varying(32) NOT NULL,
    name character varying(120) NOT NULL,
    contact_name character varying(64) NOT NULL,
    contact_phone character varying(32) NOT NULL,
    settlement_type character varying(32) NOT NULL,
    is_active boolean NOT NULL,
    wechat_group character varying(120) NOT NULL,
    billing_day integer NOT NULL,
    credit_days integer NOT NULL,
    credit_limit numeric(14,2) NOT NULL,
    category character varying(16) NOT NULL,
    level character varying(1) NOT NULL
);
CREATE TABLE public.md_driver (
    id uuid NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    is_deleted boolean NOT NULL,
    deleted_at timestamp with time zone,
    name character varying(64) NOT NULL,
    phone character varying(32) NOT NULL,
    id_no character varying(32) NOT NULL,
    license_no character varying(32) NOT NULL,
    is_active boolean NOT NULL,
    carrier_id uuid,
    license_expiry date,
    license_type character varying(16) NOT NULL,
    qualification_cert_no character varying(64) NOT NULL,
    qualification_expiry date,
    employment_type character varying(16) NOT NULL,
    app_registered boolean NOT NULL,
    app_registered_at timestamp with time zone,
    cumulative_freight numeric(14,2) NOT NULL,
    cumulative_waybills integer NOT NULL,
    wechat character varying(64) NOT NULL
);
CREATE TABLE public.md_driver_credential (
    id uuid NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    cred_type character varying(24) NOT NULL,
    side character varying(8) NOT NULL,
    file character varying(100),
    file_url character varying(200) NOT NULL,
    ocr_status character varying(16) NOT NULL,
    ocr_result jsonb NOT NULL,
    holder_name character varying(64) NOT NULL,
    cert_no character varying(64) NOT NULL,
    expiry_date date,
    self_uploaded boolean NOT NULL,
    driver_id uuid NOT NULL,
    uploaded_by_id uuid
);
CREATE TABLE public.md_route (
    id uuid NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    is_deleted boolean NOT NULL,
    deleted_at timestamp with time zone,
    code character varying(32) NOT NULL,
    name character varying(160) NOT NULL,
    origin character varying(80) NOT NULL,
    destination character varying(80) NOT NULL,
    waypoints jsonb NOT NULL,
    corridor_m numeric(10,2) NOT NULL,
    distance_km numeric(10,2) NOT NULL,
    is_active boolean NOT NULL
);
CREATE TABLE public.md_vehicle (
    id uuid NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    is_deleted boolean NOT NULL,
    deleted_at timestamp with time zone,
    plate_no character varying(32) NOT NULL,
    vehicle_type character varying(64) NOT NULL,
    ownership_type character varying(32) NOT NULL,
    is_active boolean NOT NULL,
    carrier_id uuid,
    inspection_expiry date,
    insurance_expiry date,
    maintenance_due_date date,
    road_transport_cert_no character varying(64) NOT NULL,
    load_capacity_ton numeric(10,2) NOT NULL,
    volume_capacity_cbm numeric(10,2) NOT NULL,
    vehicle_class character varying(16) NOT NULL,
    dispatch_source character varying(16) NOT NULL,
    body_type character varying(16) NOT NULL,
    vehicle_length_m numeric(4,1) NOT NULL
);
CREATE TABLE public.ntf_notification (
    id uuid NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    category character varying(48) NOT NULL,
    title character varying(160) NOT NULL,
    body character varying(255) NOT NULL,
    level character varying(16) NOT NULL,
    link_type character varying(32) NOT NULL,
    link_id character varying(64) NOT NULL,
    payload jsonb NOT NULL,
    is_read boolean NOT NULL,
    read_at timestamp with time zone,
    recipient_id uuid NOT NULL
);
CREATE TABLE public.ops_contract (
    id uuid NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    contract_no character varying(40) NOT NULL,
    template_code character varying(32) NOT NULL,
    content text NOT NULL,
    sent_at timestamp with time zone,
    driver_reply character varying(255) NOT NULL,
    confirm_status character varying(16) NOT NULL,
    confirmed_at timestamp with time zone,
    pdf character varying(100),
    driver_id uuid,
    waybill_id uuid NOT NULL
);
CREATE TABLE public.ops_dispatch_batch (
    id uuid NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    batch_no character varying(40) NOT NULL,
    dispatch_type character varying(16) NOT NULL,
    platform_name character varying(64) NOT NULL,
    status character varying(16) NOT NULL,
    allocation character varying(16) NOT NULL,
    total_payable numeric(14,2) NOT NULL,
    order_count integer NOT NULL,
    total_weight_ton numeric(12,2) NOT NULL,
    note character varying(200) NOT NULL,
    carrier_id uuid,
    created_by_id uuid,
    organization_id uuid,
    statement_no character varying(40) NOT NULL
);
CREATE TABLE public.ops_driver_checkin (
    id uuid NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    node character varying(20) NOT NULL,
    lat numeric(10,6),
    lng numeric(10,6),
    photo character varying(100),
    note character varying(255) NOT NULL,
    checkin_at timestamp with time zone NOT NULL,
    driver_id uuid,
    waybill_id uuid NOT NULL
);
CREATE TABLE public.ops_driver_reminder (
    id uuid NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    title character varying(120) NOT NULL,
    content text NOT NULL,
    ack_required boolean NOT NULL,
    status character varying(16) NOT NULL,
    sent_at timestamp with time zone NOT NULL,
    acknowledged_at timestamp with time zone,
    driver_id uuid,
    sent_by_id uuid,
    waybill_id uuid,
    template_id uuid,
    level character varying(16) NOT NULL
);
CREATE TABLE public.ops_exception (
    id uuid NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    exception_type character varying(64) NOT NULL,
    description text NOT NULL,
    status character varying(32) NOT NULL,
    responsibility_party character varying(80) NOT NULL,
    amount numeric(12,2) NOT NULL,
    waybill_id uuid,
    assignee_id uuid,
    level character varying(16) NOT NULL,
    resolution text NOT NULL,
    source character varying(32) NOT NULL,
    order_id uuid,
    reported_by_id uuid
);
CREATE TABLE public.ops_exception_event (
    id uuid NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    event_type character varying(48) NOT NULL,
    from_status character varying(32) NOT NULL,
    to_status character varying(32) NOT NULL,
    note character varying(255) NOT NULL,
    payload jsonb NOT NULL,
    event_time timestamp with time zone NOT NULL,
    actor_id uuid,
    exception_id uuid NOT NULL
);
CREATE TABLE public.ops_number_counter (
    id bigint NOT NULL,
    scope character varying(64) NOT NULL,
    value bigint NOT NULL
);
ALTER TABLE public.ops_number_counter ALTER COLUMN id ADD GENERATED BY DEFAULT AS IDENTITY (
    SEQUENCE NAME public.ops_number_counter_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);
CREATE TABLE public.ops_order (
    id uuid NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    order_no character varying(40) NOT NULL,
    source character varying(32) NOT NULL,
    status character varying(32) NOT NULL,
    remark character varying(255) NOT NULL,
    customer_id uuid,
    cargo_desc character varying(255) NOT NULL,
    cargo_quantity integer NOT NULL,
    cargo_volume_cbm numeric(10,2) NOT NULL,
    cargo_weight_ton numeric(10,2) NOT NULL,
    channel character varying(24) NOT NULL,
    contact_name character varying(64) NOT NULL,
    contact_phone character varying(32) NOT NULL,
    destination character varying(120) NOT NULL,
    expected_delivery_at timestamp with time zone,
    expected_pickup_at timestamp with time zone,
    origin character varying(120) NOT NULL,
    parse_meta jsonb NOT NULL,
    raw_text text NOT NULL,
    business_type character varying(16) NOT NULL,
    cargo_value numeric(14,2) NOT NULL,
    claimed_at timestamp with time zone,
    claimed_by_id uuid,
    created_by_id uuid,
    deleted_at timestamp with time zone,
    delivery_address character varying(255) NOT NULL,
    delivery_contact_name character varying(64) NOT NULL,
    delivery_contact_phone character varying(32) NOT NULL,
    is_deleted boolean NOT NULL,
    is_hazardous boolean NOT NULL,
    package_type character varying(32) NOT NULL,
    pickup_address character varying(255) NOT NULL,
    pickup_contact_name character varying(64) NOT NULL,
    pickup_contact_phone character varying(32) NOT NULL,
    pooled_at timestamp with time zone,
    priority character varying(16) NOT NULL,
    quoted_amount numeric(14,2) NOT NULL,
    settlement_type character varying(16) NOT NULL,
    source_type character varying(16) NOT NULL,
    temperature_range character varying(32) NOT NULL,
    delivered_at timestamp with time zone,
    sla_status character varying(16) NOT NULL,
    approval_remark character varying(255) NOT NULL,
    approval_status character varying(16) NOT NULL,
    approved_at timestamp with time zone,
    approved_by_id uuid,
    ai_conversation_id character varying(64) NOT NULL,
    cod_amount numeric(14,2) NOT NULL,
    cod_status character varying(16) NOT NULL,
    freight_payer character varying(16) NOT NULL,
    freight_term character varying(16) NOT NULL,
    assigned_at timestamp with time zone,
    assigned_by_id uuid,
    assigned_to_id uuid,
    project_id uuid
);
CREATE TABLE public.ops_order_attachment (
    id uuid NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    kind character varying(16) NOT NULL,
    name character varying(160) NOT NULL,
    file character varying(100),
    file_url character varying(200) NOT NULL,
    order_id uuid NOT NULL,
    uploaded_by_id uuid
);
CREATE TABLE public.ops_order_cargo_item (
    id uuid NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    seq integer NOT NULL,
    name character varying(120) NOT NULL,
    quantity integer NOT NULL,
    weight_ton numeric(10,2) NOT NULL,
    volume_cbm numeric(10,2) NOT NULL,
    package_type character varying(32) NOT NULL,
    temperature_range character varying(32) NOT NULL,
    remark character varying(255) NOT NULL,
    order_id uuid NOT NULL,
    CONSTRAINT ops_order_cargo_item_seq_check CHECK ((seq >= 0))
);
CREATE TABLE public.ops_order_event (
    id uuid NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    event_type character varying(48) NOT NULL,
    from_status character varying(32) NOT NULL,
    to_status character varying(32) NOT NULL,
    source character varying(24) NOT NULL,
    payload jsonb NOT NULL,
    event_time timestamp with time zone NOT NULL,
    actor_id uuid,
    order_id uuid NOT NULL
);
CREATE TABLE public.ops_order_stop (
    id uuid NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    seq integer NOT NULL,
    stop_type character varying(12) NOT NULL,
    city character varying(80) NOT NULL,
    address character varying(255) NOT NULL,
    contact_name character varying(64) NOT NULL,
    contact_phone character varying(32) NOT NULL,
    expected_start timestamp with time zone,
    expected_end timestamp with time zone,
    cargo_note character varying(255) NOT NULL,
    order_id uuid NOT NULL,
    CONSTRAINT ops_order_stop_seq_check CHECK ((seq >= 0))
);
CREATE TABLE public.ops_order_template (
    id uuid NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    is_deleted boolean NOT NULL,
    deleted_at timestamp with time zone,
    name character varying(120) NOT NULL,
    payload jsonb NOT NULL,
    created_by_id uuid
);
CREATE TABLE public.ops_receipt (
    id uuid NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    receipt_type character varying(32) NOT NULL,
    status character varying(32) NOT NULL,
    file character varying(100),
    file_url character varying(200) NOT NULL,
    ocr_status character varying(16) NOT NULL,
    ocr_result jsonb NOT NULL,
    signatory character varying(80) NOT NULL,
    signed_at timestamp with time zone,
    uploaded_by_id uuid,
    waybill_id uuid NOT NULL,
    sign_source character varying(16) NOT NULL,
    signature text NOT NULL,
    damaged_quantity numeric(12,2) NOT NULL,
    outcome character varying(16) NOT NULL,
    rejection_reason character varying(255) NOT NULL,
    shortage_quantity numeric(12,2) NOT NULL,
    signed_quantity numeric(12,2) NOT NULL,
    total_quantity numeric(12,2) NOT NULL
);
CREATE TABLE public.ops_reminder_template (
    id uuid NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    is_deleted boolean NOT NULL,
    deleted_at timestamp with time zone,
    name character varying(120) NOT NULL,
    category character varying(32) NOT NULL,
    content text NOT NULL,
    is_active boolean NOT NULL,
    created_by_id uuid
);
CREATE TABLE public.ops_tracking_point (
    id uuid NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    lng numeric(10,6) NOT NULL,
    lat numeric(10,6) NOT NULL,
    speed_kmh numeric(8,2) NOT NULL,
    reported_at timestamp with time zone NOT NULL,
    provider character varying(32) NOT NULL,
    waybill_id uuid NOT NULL
);
CREATE TABLE public.ops_waybill (
    id uuid NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    waybill_no character varying(40) NOT NULL,
    route_name character varying(160) NOT NULL,
    origin character varying(80) NOT NULL,
    destination character varying(80) NOT NULL,
    status character varying(32) NOT NULL,
    dispatch_status character varying(32) NOT NULL,
    risk_level character varying(16) NOT NULL,
    receipt_status character varying(32) NOT NULL,
    eta_drift_minutes integer NOT NULL,
    cargo_quantity integer NOT NULL,
    cargo_weight_ton numeric(10,2) NOT NULL,
    cargo_volume_cbm numeric(10,2) NOT NULL,
    planned_arrival timestamp with time zone,
    estimated_arrival timestamp with time zone,
    carrier_id uuid,
    customer_id uuid,
    driver_id uuid,
    order_id uuid,
    organization_id uuid,
    vehicle_id uuid,
    planned_route_id uuid,
    parent_id uuid,
    dispatch_type character varying(16) NOT NULL,
    trailer_id uuid,
    arrived_at timestamp with time zone,
    departed_at timestamp with time zone,
    loaded_at timestamp with time zone,
    signed_at timestamp with time zone,
    ai_conversation_id character varying(64) NOT NULL,
    cod_amount numeric(14,2) NOT NULL,
    cod_collected_at timestamp with time zone,
    cod_remitted_at timestamp with time zone,
    cod_status character varying(16) NOT NULL,
    freight_payer character varying(16) NOT NULL,
    freight_term character varying(16) NOT NULL,
    platform_name character varying(64) NOT NULL,
    platform_order_no character varying(64) NOT NULL,
    batch_id uuid,
    project_id uuid
);
CREATE TABLE public.ops_waybill_driver (
    id uuid NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    role character varying(12) NOT NULL,
    note character varying(120) NOT NULL,
    driver_id uuid NOT NULL,
    waybill_id uuid NOT NULL
);
CREATE TABLE public.ops_waybill_event (
    id uuid NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    event_type character varying(64) NOT NULL,
    event_time timestamp with time zone NOT NULL,
    resource character varying(80) NOT NULL,
    source character varying(32) NOT NULL,
    payload jsonb NOT NULL,
    waybill_id uuid NOT NULL
);
CREATE TABLE public.ops_waybill_stop (
    id uuid NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    seq integer NOT NULL,
    stop_type character varying(12) NOT NULL,
    city character varying(80) NOT NULL,
    address character varying(255) NOT NULL,
    contact_name character varying(64) NOT NULL,
    contact_phone character varying(32) NOT NULL,
    lat numeric(10,6),
    lng numeric(10,6),
    radius_m integer NOT NULL,
    planned_eta timestamp with time zone,
    actual_arrival_at timestamp with time zone,
    actual_depart_at timestamp with time zone,
    arrival_source character varying(12) NOT NULL,
    status character varying(12) NOT NULL,
    note character varying(255) NOT NULL,
    waybill_id uuid NOT NULL,
    CONSTRAINT ops_waybill_stop_radius_m_check CHECK ((radius_m >= 0)),
    CONSTRAINT ops_waybill_stop_seq_check CHECK ((seq >= 0))
);
CREATE TABLE public.tel_alert (
    id uuid NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    alert_type character varying(24) NOT NULL,
    level character varying(16) NOT NULL,
    status character varying(16) NOT NULL,
    message character varying(255) NOT NULL,
    value numeric(12,2),
    threshold numeric(12,2),
    detail jsonb NOT NULL,
    triggered_at timestamp with time zone NOT NULL,
    handled_at timestamp with time zone,
    handled_by_id uuid,
    vehicle_id uuid,
    waybill_id uuid,
    device_id uuid
);
CREATE TABLE public.tel_device (
    id uuid NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    device_no character varying(64) NOT NULL,
    device_type character varying(16) NOT NULL,
    sim_no character varying(32) NOT NULL,
    status character varying(16) NOT NULL,
    last_seen_at timestamp with time zone,
    meta jsonb NOT NULL,
    vehicle_id uuid
);
CREATE TABLE public.tel_geofence (
    id uuid NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    name character varying(120) NOT NULL,
    shape character varying(16) NOT NULL,
    purpose character varying(16) NOT NULL,
    center_lng numeric(10,6),
    center_lat numeric(10,6),
    radius_m numeric(10,2) NOT NULL,
    polygon jsonb NOT NULL,
    is_active boolean NOT NULL
);
CREATE TABLE public.tel_geofence_state (
    id uuid NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    inside boolean NOT NULL,
    since timestamp with time zone,
    geofence_id uuid NOT NULL,
    vehicle_id uuid NOT NULL
);
CREATE TABLE public.tel_vehicle_state (
    id uuid NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    lng numeric(10,6) NOT NULL,
    lat numeric(10,6) NOT NULL,
    speed_kmh numeric(8,2) NOT NULL,
    heading integer NOT NULL,
    mileage_km numeric(12,2) NOT NULL,
    temperature_c numeric(6,2),
    fuel_pct numeric(5,2),
    online boolean NOT NULL,
    reported_at timestamp with time zone,
    vehicle_id uuid NOT NULL,
    waybill_id uuid
);
ALTER TABLE ONLY public.ai_agent_thread_message ALTER COLUMN seq SET DEFAULT nextval('public.ai_agent_thread_message_seq_seq'::regclass);
ALTER TABLE ONLY public.accounts_user
    ADD CONSTRAINT accounts_user_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.accounts_user
    ADD CONSTRAINT accounts_user_username_key UNIQUE (username);
ALTER TABLE ONLY public.ai_agent_suggestion
    ADD CONSTRAINT ai_agent_suggestion_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.ai_agent_thread_message
    ADD CONSTRAINT ai_agent_thread_message_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.ana_metric_snapshot
    ADD CONSTRAINT ana_metric_snapshot_metric_code_stat_date_di_55995d21_uniq UNIQUE (metric_code, stat_date, dimension_key);
ALTER TABLE ONLY public.ana_metric_snapshot
    ADD CONSTRAINT ana_metric_snapshot_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.audit_log
    ADD CONSTRAINT audit_log_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.fin_contract
    ADD CONSTRAINT fin_contract_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.fin_expense_item
    ADD CONSTRAINT fin_expense_item_code_key UNIQUE (code);
ALTER TABLE ONLY public.fin_expense_item
    ADD CONSTRAINT fin_expense_item_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.fin_expense_record
    ADD CONSTRAINT fin_expense_record_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.fin_payment_request
    ADD CONSTRAINT fin_payment_request_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.fin_payment_request
    ADD CONSTRAINT fin_payment_request_request_no_key UNIQUE (request_no);
ALTER TABLE ONLY public.fin_pricing_rule
    ADD CONSTRAINT fin_pricing_rule_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.fin_project
    ADD CONSTRAINT fin_project_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.fin_reimbursement
    ADD CONSTRAINT fin_reimbursement_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.fin_reimbursement
    ADD CONSTRAINT fin_reimbursement_reimb_no_key UNIQUE (reimb_no);
ALTER TABLE ONLY public.fin_statement_line
    ADD CONSTRAINT fin_statement_line_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.fin_statement_payment
    ADD CONSTRAINT fin_statement_payment_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.fin_statement
    ADD CONSTRAINT fin_statement_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.fin_statement
    ADD CONSTRAINT fin_statement_statement_no_key UNIQUE (statement_no);
ALTER TABLE ONLY public.fin_webhook_delivery
    ADD CONSTRAINT fin_webhook_delivery_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.fin_webhook
    ADD CONSTRAINT fin_webhook_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.iam_account_handover
    ADD CONSTRAINT iam_account_handover_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.iam_api_key
    ADD CONSTRAINT iam_api_key_key_id_key UNIQUE (key_id);
ALTER TABLE ONLY public.iam_api_key
    ADD CONSTRAINT iam_api_key_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.iam_department
    ADD CONSTRAINT iam_department_organization_id_code_53783978_uniq UNIQUE (organization_id, code);
ALTER TABLE ONLY public.iam_department
    ADD CONSTRAINT iam_department_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.iam_employee
    ADD CONSTRAINT iam_employee_employee_no_key UNIQUE (employee_no);
ALTER TABLE ONLY public.iam_employee_group
    ADD CONSTRAINT iam_employee_group_code_key UNIQUE (code);
ALTER TABLE ONLY public.iam_employee_group
    ADD CONSTRAINT iam_employee_group_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.iam_employee_group_roles
    ADD CONSTRAINT iam_employee_group_roles_employeegroup_id_role_id_e84e9236_uniq UNIQUE (employeegroup_id, role_id);
ALTER TABLE ONLY public.iam_employee_group_roles
    ADD CONSTRAINT iam_employee_group_roles_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.iam_employee_groups
    ADD CONSTRAINT iam_employee_groups_employee_id_employeegroup_id_9d0ec78c_uniq UNIQUE (employee_id, employeegroup_id);
ALTER TABLE ONLY public.iam_employee_groups
    ADD CONSTRAINT iam_employee_groups_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.iam_employee
    ADD CONSTRAINT iam_employee_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.iam_employee
    ADD CONSTRAINT iam_employee_user_id_key UNIQUE (user_id);
ALTER TABLE ONLY public.iam_login_attempt
    ADD CONSTRAINT iam_login_attempt_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.iam_organization
    ADD CONSTRAINT iam_organization_code_key UNIQUE (code);
ALTER TABLE ONLY public.iam_organization
    ADD CONSTRAINT iam_organization_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.iam_permission
    ADD CONSTRAINT iam_permission_code_key UNIQUE (code);
ALTER TABLE ONLY public.iam_permission
    ADD CONSTRAINT iam_permission_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.iam_role_assignment
    ADD CONSTRAINT iam_role_assignment_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.iam_role_assignment
    ADD CONSTRAINT iam_role_assignment_user_id_role_id_organiza_26aa1be8_uniq UNIQUE (user_id, role_id, organization_id);
ALTER TABLE ONLY public.iam_role
    ADD CONSTRAINT iam_role_code_key UNIQUE (code);
ALTER TABLE ONLY public.iam_role_permissions
    ADD CONSTRAINT iam_role_permissions_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.iam_role_permissions
    ADD CONSTRAINT iam_role_permissions_role_id_permission_id_d75691c1_uniq UNIQUE (role_id, permission_id);
ALTER TABLE ONLY public.iam_role
    ADD CONSTRAINT iam_role_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.iam_service_area
    ADD CONSTRAINT iam_service_area_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.md_b2b_partner
    ADD CONSTRAINT md_b2b_partner_code_key UNIQUE (code);
ALTER TABLE ONLY public.md_b2b_partner
    ADD CONSTRAINT md_b2b_partner_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.md_carrier
    ADD CONSTRAINT md_carrier_code_key UNIQUE (code);
ALTER TABLE ONLY public.md_carrier_lane_price
    ADD CONSTRAINT md_carrier_lane_price_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.md_carrier
    ADD CONSTRAINT md_carrier_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.md_customer
    ADD CONSTRAINT md_customer_code_key UNIQUE (code);
ALTER TABLE ONLY public.md_customer
    ADD CONSTRAINT md_customer_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.md_driver_credential
    ADD CONSTRAINT md_driver_credential_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.md_driver
    ADD CONSTRAINT md_driver_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.md_route
    ADD CONSTRAINT md_route_code_key UNIQUE (code);
ALTER TABLE ONLY public.md_route
    ADD CONSTRAINT md_route_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.md_vehicle
    ADD CONSTRAINT md_vehicle_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.md_vehicle
    ADD CONSTRAINT md_vehicle_plate_no_key UNIQUE (plate_no);
ALTER TABLE ONLY public.ntf_notification
    ADD CONSTRAINT ntf_notification_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.ops_contract
    ADD CONSTRAINT ops_contract_contract_no_key UNIQUE (contract_no);
ALTER TABLE ONLY public.ops_contract
    ADD CONSTRAINT ops_contract_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.ops_dispatch_batch
    ADD CONSTRAINT ops_dispatch_batch_batch_no_key UNIQUE (batch_no);
ALTER TABLE ONLY public.ops_dispatch_batch
    ADD CONSTRAINT ops_dispatch_batch_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.ops_driver_checkin
    ADD CONSTRAINT ops_driver_checkin_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.ops_driver_reminder
    ADD CONSTRAINT ops_driver_reminder_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.ops_exception_event
    ADD CONSTRAINT ops_exception_event_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.ops_exception
    ADD CONSTRAINT ops_exception_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.ops_number_counter
    ADD CONSTRAINT ops_number_counter_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.ops_number_counter
    ADD CONSTRAINT ops_number_counter_scope_key UNIQUE (scope);
ALTER TABLE ONLY public.ops_order_attachment
    ADD CONSTRAINT ops_order_attachment_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.ops_order_cargo_item
    ADD CONSTRAINT ops_order_cargo_item_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.ops_order_event
    ADD CONSTRAINT ops_order_event_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.ops_order
    ADD CONSTRAINT ops_order_order_no_key UNIQUE (order_no);
ALTER TABLE ONLY public.ops_order
    ADD CONSTRAINT ops_order_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.ops_order_stop
    ADD CONSTRAINT ops_order_stop_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.ops_order_template
    ADD CONSTRAINT ops_order_template_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.ops_receipt
    ADD CONSTRAINT ops_receipt_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.ops_reminder_template
    ADD CONSTRAINT ops_reminder_template_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.ops_tracking_point
    ADD CONSTRAINT ops_tracking_point_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.ops_waybill_driver
    ADD CONSTRAINT ops_waybill_driver_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.ops_waybill_event
    ADD CONSTRAINT ops_waybill_event_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.ops_waybill
    ADD CONSTRAINT ops_waybill_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.ops_waybill_stop
    ADD CONSTRAINT ops_waybill_stop_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.ops_waybill
    ADD CONSTRAINT ops_waybill_waybill_no_key UNIQUE (waybill_no);
ALTER TABLE ONLY public.tel_alert
    ADD CONSTRAINT tel_alert_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.tel_device
    ADD CONSTRAINT tel_device_device_no_key UNIQUE (device_no);
ALTER TABLE ONLY public.tel_device
    ADD CONSTRAINT tel_device_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.tel_geofence
    ADD CONSTRAINT tel_geofence_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.tel_geofence_state
    ADD CONSTRAINT tel_geofence_state_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.tel_geofence_state
    ADD CONSTRAINT tel_geofence_state_vehicle_id_geofence_id_7d0e74ca_uniq UNIQUE (vehicle_id, geofence_id);
ALTER TABLE ONLY public.tel_vehicle_state
    ADD CONSTRAINT tel_vehicle_state_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.tel_vehicle_state
    ADD CONSTRAINT tel_vehicle_state_vehicle_id_key UNIQUE (vehicle_id);
ALTER TABLE ONLY public.ops_waybill_driver
    ADD CONSTRAINT uniq_waybill_driver UNIQUE (waybill_id, driver_id);
CREATE INDEX accounts_user_organization_id_d808f5ca ON public.accounts_user USING btree (organization_id);
CREATE INDEX accounts_user_phone_c603acdd ON public.accounts_user USING btree (phone);
CREATE INDEX accounts_user_phone_c603acdd_like ON public.accounts_user USING btree (phone varchar_pattern_ops);
CREATE INDEX accounts_user_username_6088629e_like ON public.accounts_user USING btree (username varchar_pattern_ops);
CREATE INDEX ai_agent_su_suggest_412a49_idx ON public.ai_agent_suggestion USING btree (suggestion_type, status);
CREATE INDEX ai_agent_suggestion_confirmed_by_id_5bdc4ec0 ON public.ai_agent_suggestion USING btree (confirmed_by_id);
CREATE INDEX ai_agent_suggestion_created_at_a0768a55 ON public.ai_agent_suggestion USING btree (created_at);
CREATE INDEX ai_agent_suggestion_waybill_id_fe46fe12 ON public.ai_agent_suggestion USING btree (waybill_id);
CREATE INDEX ai_agent_thread_message_thread_idx ON public.ai_agent_thread_message USING btree (thread_id, seq);
CREATE INDEX ana_metric_snapshot_created_at_780f8c27 ON public.ana_metric_snapshot USING btree (created_at);
CREATE INDEX ana_metric_snapshot_metric_code_454f33de ON public.ana_metric_snapshot USING btree (metric_code);
CREATE INDEX ana_metric_snapshot_metric_code_454f33de_like ON public.ana_metric_snapshot USING btree (metric_code varchar_pattern_ops);
CREATE INDEX ana_metric_snapshot_stat_date_3508eb80 ON public.ana_metric_snapshot USING btree (stat_date);
CREATE INDEX audit_log_action_97df64_idx ON public.audit_log USING btree (action, created_at);
CREATE INDEX audit_log_actor_id_dfa07704 ON public.audit_log USING btree (actor_id);
CREATE INDEX audit_log_created_at_24c1f1be ON public.audit_log USING btree (created_at);
CREATE INDEX audit_log_request_id_e1cc018e ON public.audit_log USING btree (request_id);
CREATE INDEX audit_log_request_id_e1cc018e_like ON public.audit_log USING btree (request_id varchar_pattern_ops);
CREATE INDEX audit_log_resourc_2570dd_idx ON public.audit_log USING btree (resource_type, resource_id);
CREATE UNIQUE INDEX fin_contract_no_key ON public.fin_contract USING btree (contract_no);
CREATE INDEX fin_contract_party_idx ON public.fin_contract USING btree (party_type, party_id, status, effective_from DESC);
CREATE INDEX fin_expense_directi_cc9aef_idx ON public.fin_expense_record USING btree (direction, risk_status);
CREATE INDEX fin_expense_item_code_b23527a3_like ON public.fin_expense_item USING btree (code varchar_pattern_ops);
CREATE INDEX fin_expense_item_created_at_6e5e3a1e ON public.fin_expense_item USING btree (created_at);
CREATE INDEX fin_expense_record_created_at_3e65b217 ON public.fin_expense_record USING btree (created_at);
CREATE INDEX fin_expense_record_payee_type_18b49143 ON public.fin_expense_record USING btree (payee_type);
CREATE INDEX fin_expense_record_payee_type_18b49143_like ON public.fin_expense_record USING btree (payee_type varchar_pattern_ops);
CREATE INDEX fin_expense_record_price_source_b635aee2 ON public.fin_expense_record USING btree (price_source);
CREATE INDEX fin_expense_record_price_source_b635aee2_like ON public.fin_expense_record USING btree (price_source varchar_pattern_ops);
CREATE INDEX fin_expense_record_waybill_id_1b06c61b ON public.fin_expense_record USING btree (waybill_id);
CREATE INDEX fin_expense_waybill_fff84d_idx ON public.fin_expense_record USING btree (waybill_id, direction);
CREATE INDEX fin_payment_request_created_at_add5ad9a ON public.fin_payment_request USING btree (created_at);
CREATE INDEX fin_payment_request_request_no_85aa40db_like ON public.fin_payment_request USING btree (request_no varchar_pattern_ops);
CREATE INDEX fin_payment_request_waybill_id_148a8045 ON public.fin_payment_request USING btree (waybill_id);
CREATE INDEX fin_payment_status_ed7ada_idx ON public.fin_payment_request USING btree (status);
CREATE INDEX fin_pricing_price_t_337b53_idx ON public.fin_pricing_rule USING btree (price_type, is_active);
CREATE INDEX fin_pricing_rule_carrier_id_69055060 ON public.fin_pricing_rule USING btree (carrier_id);
CREATE INDEX fin_pricing_rule_contract_idx ON public.fin_pricing_rule USING btree (contract_id);
CREATE INDEX fin_pricing_rule_created_at_9ba49d8f ON public.fin_pricing_rule USING btree (created_at);
CREATE INDEX fin_pricing_rule_customer_id_c65ddc40 ON public.fin_pricing_rule USING btree (customer_id);
CREATE INDEX fin_project_customer_idx ON public.fin_project USING btree (customer_id, status);
CREATE UNIQUE INDEX fin_project_no_key ON public.fin_project USING btree (project_no);
CREATE INDEX fin_reimbur_status_d70e71_idx ON public.fin_reimbursement USING btree (status);
CREATE INDEX fin_reimbur_waybill_27c9ce_idx ON public.fin_reimbursement USING btree (waybill_id, status);
CREATE INDEX fin_reimbursement_approved_by_id_2d125c06 ON public.fin_reimbursement USING btree (approved_by_id);
CREATE INDEX fin_reimbursement_category_8b36aaea ON public.fin_reimbursement USING btree (category);
CREATE INDEX fin_reimbursement_category_8b36aaea_like ON public.fin_reimbursement USING btree (category varchar_pattern_ops);
CREATE INDEX fin_reimbursement_created_at_e0bba639 ON public.fin_reimbursement USING btree (created_at);
CREATE INDEX fin_reimbursement_payment_request_id_a37eef7a ON public.fin_reimbursement USING btree (payment_request_id);
CREATE INDEX fin_reimbursement_reimb_no_bc5c1f26_like ON public.fin_reimbursement USING btree (reimb_no varchar_pattern_ops);
CREATE INDEX fin_reimbursement_status_cb922424 ON public.fin_reimbursement USING btree (status);
CREATE INDEX fin_reimbursement_status_cb922424_like ON public.fin_reimbursement USING btree (status varchar_pattern_ops);
CREATE INDEX fin_reimbursement_submitted_by_id_139934a1 ON public.fin_reimbursement USING btree (submitted_by_id);
CREATE INDEX fin_reimbursement_waybill_id_1d285669 ON public.fin_reimbursement USING btree (waybill_id);
CREATE INDEX fin_stateme_counter_7264fc_idx ON public.fin_statement USING btree (counterparty_type, counterparty_id);
CREATE INDEX fin_stateme_directi_d5c98d_idx ON public.fin_statement USING btree (direction, status);
CREATE INDEX fin_stateme_stateme_9078a3_idx ON public.fin_statement_payment USING btree (statement_id);
CREATE INDEX fin_statement_confirmed_by_id_5f724e70 ON public.fin_statement USING btree (confirmed_by_id);
CREATE INDEX fin_statement_created_at_66f6caa2 ON public.fin_statement USING btree (created_at);
CREATE INDEX fin_statement_line_created_at_b0df95d6 ON public.fin_statement_line USING btree (created_at);
CREATE INDEX fin_statement_line_expense_record_id_fabfd20c ON public.fin_statement_line USING btree (expense_record_id);
CREATE UNIQUE INDEX fin_statement_line_expense_uniq ON public.fin_statement_line USING btree (expense_record_id) WHERE (expense_record_id IS NOT NULL);
CREATE INDEX fin_statement_line_statement_id_d115bccc ON public.fin_statement_line USING btree (statement_id);
CREATE INDEX fin_statement_payment_created_at_e9fb0d44 ON public.fin_statement_payment USING btree (created_at);
CREATE INDEX fin_statement_payment_created_by_id_8debb014 ON public.fin_statement_payment USING btree (created_by_id);
CREATE INDEX fin_statement_payment_statement_id_562b6b92 ON public.fin_statement_payment USING btree (statement_id);
CREATE INDEX fin_statement_statement_no_b134bfbf_like ON public.fin_statement USING btree (statement_no varchar_pattern_ops);
CREATE INDEX fin_webhook_created_at_b61fd3a9 ON public.fin_webhook USING btree (created_at);
CREATE INDEX fin_webhook_delivery_created_at_1fd9c606 ON public.fin_webhook_delivery USING btree (created_at);
CREATE INDEX fin_webhook_delivery_webhook_id_1f594cf2 ON public.fin_webhook_delivery USING btree (webhook_id);
CREATE INDEX fin_webhook_status_2b2f1b_idx ON public.fin_webhook_delivery USING btree (status);
CREATE INDEX iam_account_handover_created_at_8c92d642 ON public.iam_account_handover USING btree (created_at);
CREATE INDEX iam_account_handover_from_employee_id_22dcddf8 ON public.iam_account_handover USING btree (from_employee_id);
CREATE INDEX iam_account_handover_operator_id_3633570b ON public.iam_account_handover USING btree (operator_id);
CREATE INDEX iam_account_handover_to_employee_id_de92dbd2 ON public.iam_account_handover USING btree (to_employee_id);
CREATE INDEX iam_api_key_created_at_07d8ef4d ON public.iam_api_key USING btree (created_at);
CREATE INDEX iam_api_key_key_id_a4062518_like ON public.iam_api_key USING btree (key_id varchar_pattern_ops);
CREATE INDEX iam_api_key_organization_id_7e6f7f98 ON public.iam_api_key USING btree (organization_id);
CREATE INDEX iam_department_created_at_f38c78b1 ON public.iam_department USING btree (created_at);
CREATE INDEX iam_department_manager_id_dc3369cb ON public.iam_department USING btree (manager_id);
CREATE INDEX iam_department_organization_id_8ff6605f ON public.iam_department USING btree (organization_id);
CREATE INDEX iam_department_parent_id_6e292384 ON public.iam_department USING btree (parent_id);
CREATE INDEX iam_employe_organiz_2dbc79_idx ON public.iam_employee USING btree (organization_id, status);
CREATE INDEX iam_employe_status_a79826_idx ON public.iam_employee USING btree (status);
CREATE INDEX iam_employee_created_at_05a5d0d6 ON public.iam_employee USING btree (created_at);
CREATE INDEX iam_employee_department_id_5f79239f ON public.iam_employee USING btree (department_id);
CREATE INDEX iam_employee_employee_no_cab92948_like ON public.iam_employee USING btree (employee_no varchar_pattern_ops);
CREATE INDEX iam_employee_group_code_eeb2c0a5_like ON public.iam_employee_group USING btree (code varchar_pattern_ops);
CREATE INDEX iam_employee_group_created_at_d2482057 ON public.iam_employee_group USING btree (created_at);
CREATE INDEX iam_employee_group_roles_employeegroup_id_b4f326a5 ON public.iam_employee_group_roles USING btree (employeegroup_id);
CREATE INDEX iam_employee_group_roles_role_id_33b873e7 ON public.iam_employee_group_roles USING btree (role_id);
CREATE INDEX iam_employee_groups_employee_id_38929552 ON public.iam_employee_groups USING btree (employee_id);
CREATE INDEX iam_employee_groups_employeegroup_id_7a9003d2 ON public.iam_employee_groups USING btree (employeegroup_id);
CREATE INDEX iam_employee_organization_id_55536134 ON public.iam_employee USING btree (organization_id);
CREATE INDEX iam_employee_phone_5124d0cc ON public.iam_employee USING btree (phone);
CREATE INDEX iam_employee_phone_5124d0cc_like ON public.iam_employee USING btree (phone varchar_pattern_ops);
CREATE INDEX iam_employee_supervisor_id_a5b0f24f ON public.iam_employee USING btree (supervisor_id);
CREATE INDEX iam_login_a_success_35208f_idx ON public.iam_login_attempt USING btree (success, created_at);
CREATE INDEX iam_login_a_usernam_6419ca_idx ON public.iam_login_attempt USING btree (username, created_at);
CREATE INDEX iam_login_attempt_created_at_62a09647 ON public.iam_login_attempt USING btree (created_at);
CREATE INDEX iam_login_attempt_user_id_57aa6b8e ON public.iam_login_attempt USING btree (user_id);
CREATE INDEX iam_login_attempt_username_73fc3d13 ON public.iam_login_attempt USING btree (username);
CREATE INDEX iam_login_attempt_username_73fc3d13_like ON public.iam_login_attempt USING btree (username varchar_pattern_ops);
CREATE INDEX iam_organization_code_812dcebc_like ON public.iam_organization USING btree (code varchar_pattern_ops);
CREATE INDEX iam_organization_created_at_4e9f4b93 ON public.iam_organization USING btree (created_at);
CREATE INDEX iam_organization_parent_id_6316596a ON public.iam_organization USING btree (parent_id);
CREATE INDEX iam_organization_path_97859685 ON public.iam_organization USING btree (path);
CREATE INDEX iam_organization_path_97859685_like ON public.iam_organization USING btree (path varchar_pattern_ops);
CREATE INDEX iam_permission_code_a72d7307_like ON public.iam_permission USING btree (code varchar_pattern_ops);
CREATE INDEX iam_permission_created_at_2467cd68 ON public.iam_permission USING btree (created_at);
CREATE INDEX iam_role_assignment_created_at_fbb67ef0 ON public.iam_role_assignment USING btree (created_at);
CREATE INDEX iam_role_assignment_organization_id_1cf5de14 ON public.iam_role_assignment USING btree (organization_id);
CREATE INDEX iam_role_assignment_role_id_b9205b1c ON public.iam_role_assignment USING btree (role_id);
CREATE INDEX iam_role_assignment_user_id_18f31edd ON public.iam_role_assignment USING btree (user_id);
CREATE INDEX iam_role_code_9b908e35_like ON public.iam_role USING btree (code varchar_pattern_ops);
CREATE INDEX iam_role_created_at_863f5c4d ON public.iam_role USING btree (created_at);
CREATE INDEX iam_role_permissions_permission_id_8409a90e ON public.iam_role_permissions USING btree (permission_id);
CREATE INDEX iam_role_permissions_role_id_5c73a598 ON public.iam_role_permissions USING btree (role_id);
CREATE INDEX iam_service_area_created_at_cb9108e7 ON public.iam_service_area USING btree (created_at);
CREATE INDEX iam_service_area_organization_id_b4835aaf ON public.iam_service_area USING btree (organization_id);
CREATE INDEX iam_service_organiz_04089b_idx ON public.iam_service_area USING btree (organization_id, area_type);
CREATE INDEX iam_service_region__90b267_idx ON public.iam_service_area USING btree (region_code);
CREATE INDEX md_b2b_partner_code_ed39b0af_like ON public.md_b2b_partner USING btree (code varchar_pattern_ops);
CREATE INDEX md_b2b_partner_created_at_7e746334 ON public.md_b2b_partner USING btree (created_at);
CREATE INDEX md_b2b_partner_is_deleted_0fc51ead ON public.md_b2b_partner USING btree (is_deleted);
CREATE INDEX md_b2b_partner_partner_type_87d29dce ON public.md_b2b_partner USING btree (partner_type);
CREATE INDEX md_b2b_partner_partner_type_87d29dce_like ON public.md_b2b_partner USING btree (partner_type varchar_pattern_ops);
CREATE INDEX md_carrier__origin__d49214_idx ON public.md_carrier_lane_price USING btree (origin_city, dest_city, is_active);
CREATE INDEX md_carrier_carrier_type_c3b9e213 ON public.md_carrier USING btree (carrier_type);
CREATE INDEX md_carrier_carrier_type_c3b9e213_like ON public.md_carrier USING btree (carrier_type varchar_pattern_ops);
CREATE INDEX md_carrier_code_7c930cf1_like ON public.md_carrier USING btree (code varchar_pattern_ops);
CREATE INDEX md_carrier_created_at_04768b56 ON public.md_carrier USING btree (created_at);
CREATE INDEX md_carrier_is_deleted_283ef69f ON public.md_carrier USING btree (is_deleted);
CREATE INDEX md_carrier_lane_price_carrier_id_159f0af9 ON public.md_carrier_lane_price USING btree (carrier_id);
CREATE INDEX md_carrier_lane_price_created_at_2f5b7c7f ON public.md_carrier_lane_price USING btree (created_at);
CREATE INDEX md_carrier_lane_price_dest_city_40577d2d ON public.md_carrier_lane_price USING btree (dest_city);
CREATE INDEX md_carrier_lane_price_dest_city_40577d2d_like ON public.md_carrier_lane_price USING btree (dest_city varchar_pattern_ops);
CREATE INDEX md_carrier_lane_price_is_deleted_4bf09e2b ON public.md_carrier_lane_price USING btree (is_deleted);
CREATE INDEX md_carrier_lane_price_origin_city_24c09e00 ON public.md_carrier_lane_price USING btree (origin_city);
CREATE INDEX md_carrier_lane_price_origin_city_24c09e00_like ON public.md_carrier_lane_price USING btree (origin_city varchar_pattern_ops);
CREATE INDEX md_customer_code_e84fe1f2_like ON public.md_customer USING btree (code varchar_pattern_ops);
CREATE INDEX md_customer_created_at_54df2e88 ON public.md_customer USING btree (created_at);
CREATE INDEX md_customer_is_deleted_d9d072d0 ON public.md_customer USING btree (is_deleted);
CREATE INDEX md_customer_level_ab2a822c ON public.md_customer USING btree (level);
CREATE INDEX md_customer_level_ab2a822c_like ON public.md_customer USING btree (level varchar_pattern_ops);
CREATE INDEX md_driver_app_registered_4332f740 ON public.md_driver USING btree (app_registered);
CREATE INDEX md_driver_c_driver__395968_idx ON public.md_driver_credential USING btree (driver_id, cred_type);
CREATE INDEX md_driver_c_expiry__62e24e_idx ON public.md_driver_credential USING btree (expiry_date);
CREATE INDEX md_driver_carrier_id_a9f18029 ON public.md_driver USING btree (carrier_id);
CREATE INDEX md_driver_created_at_3063e987 ON public.md_driver USING btree (created_at);
CREATE INDEX md_driver_credential_created_at_c3bc2385 ON public.md_driver_credential USING btree (created_at);
CREATE INDEX md_driver_credential_cred_type_f1389962 ON public.md_driver_credential USING btree (cred_type);
CREATE INDEX md_driver_credential_cred_type_f1389962_like ON public.md_driver_credential USING btree (cred_type varchar_pattern_ops);
CREATE INDEX md_driver_credential_driver_id_98614c03 ON public.md_driver_credential USING btree (driver_id);
CREATE INDEX md_driver_credential_uploaded_by_id_bce43bf9 ON public.md_driver_credential USING btree (uploaded_by_id);
CREATE INDEX md_driver_employment_type_30a21b38 ON public.md_driver USING btree (employment_type);
CREATE INDEX md_driver_employment_type_30a21b38_like ON public.md_driver USING btree (employment_type varchar_pattern_ops);
CREATE INDEX md_driver_is_deleted_c1cf54b0 ON public.md_driver USING btree (is_deleted);
CREATE INDEX md_driver_phone_e1fcf6d8 ON public.md_driver USING btree (phone);
CREATE INDEX md_driver_phone_e1fcf6d8_like ON public.md_driver USING btree (phone varchar_pattern_ops);
CREATE INDEX md_route_code_f4ada9cc_like ON public.md_route USING btree (code varchar_pattern_ops);
CREATE INDEX md_route_created_at_1fc30905 ON public.md_route USING btree (created_at);
CREATE INDEX md_route_is_deleted_ac561ea6 ON public.md_route USING btree (is_deleted);
CREATE INDEX md_vehicle_carrier_id_9902574d ON public.md_vehicle USING btree (carrier_id);
CREATE INDEX md_vehicle_created_at_a547a676 ON public.md_vehicle USING btree (created_at);
CREATE INDEX md_vehicle_dispatch_source_dc81dcc3 ON public.md_vehicle USING btree (dispatch_source);
CREATE INDEX md_vehicle_dispatch_source_dc81dcc3_like ON public.md_vehicle USING btree (dispatch_source varchar_pattern_ops);
CREATE INDEX md_vehicle_is_deleted_0ab5d172 ON public.md_vehicle USING btree (is_deleted);
CREATE INDEX md_vehicle_plate_no_2a5a828c_like ON public.md_vehicle USING btree (plate_no varchar_pattern_ops);
CREATE INDEX md_vehicle_vehicle_class_f50f3f67 ON public.md_vehicle USING btree (vehicle_class);
CREATE INDEX md_vehicle_vehicle_class_f50f3f67_like ON public.md_vehicle USING btree (vehicle_class varchar_pattern_ops);
CREATE INDEX ntf_notific_recipie_8f3ac2_idx ON public.ntf_notification USING btree (recipient_id, is_read);
CREATE INDEX ntf_notification_category_ba22e255 ON public.ntf_notification USING btree (category);
CREATE INDEX ntf_notification_category_ba22e255_like ON public.ntf_notification USING btree (category varchar_pattern_ops);
CREATE INDEX ntf_notification_created_at_a19bdc99 ON public.ntf_notification USING btree (created_at);
CREATE INDEX ntf_notification_is_read_9972f151 ON public.ntf_notification USING btree (is_read);
CREATE INDEX ntf_notification_recipient_id_539bed50 ON public.ntf_notification USING btree (recipient_id);
CREATE INDEX ops_contrac_waybill_4bfe2d_idx ON public.ops_contract USING btree (waybill_id, confirm_status);
CREATE INDEX ops_contract_confirm_status_83fc99fd ON public.ops_contract USING btree (confirm_status);
CREATE INDEX ops_contract_confirm_status_83fc99fd_like ON public.ops_contract USING btree (confirm_status varchar_pattern_ops);
CREATE INDEX ops_contract_contract_no_f30fb768_like ON public.ops_contract USING btree (contract_no varchar_pattern_ops);
CREATE INDEX ops_contract_created_at_d69d0cca ON public.ops_contract USING btree (created_at);
CREATE INDEX ops_contract_driver_id_753d8137 ON public.ops_contract USING btree (driver_id);
CREATE INDEX ops_contract_waybill_id_ffe3e83c ON public.ops_contract USING btree (waybill_id);
CREATE INDEX ops_dispatc_carrier_9b056f_idx ON public.ops_dispatch_batch USING btree (carrier_id, status);
CREATE INDEX ops_dispatc_status_42cb37_idx ON public.ops_dispatch_batch USING btree (status, created_at DESC);
CREATE INDEX ops_dispatch_batch_batch_no_4a43cebd_like ON public.ops_dispatch_batch USING btree (batch_no varchar_pattern_ops);
CREATE INDEX ops_dispatch_batch_carrier_id_47dec5b2 ON public.ops_dispatch_batch USING btree (carrier_id);
CREATE INDEX ops_dispatch_batch_created_at_fa87d7c5 ON public.ops_dispatch_batch USING btree (created_at);
CREATE INDEX ops_dispatch_batch_created_by_id_8981a4b4 ON public.ops_dispatch_batch USING btree (created_by_id);
CREATE INDEX ops_dispatch_batch_organization_id_fca372ae ON public.ops_dispatch_batch USING btree (organization_id);
CREATE INDEX ops_dispatch_batch_statement_no_1f69cd65 ON public.ops_dispatch_batch USING btree (statement_no);
CREATE INDEX ops_dispatch_batch_statement_no_1f69cd65_like ON public.ops_dispatch_batch USING btree (statement_no varchar_pattern_ops);
CREATE INDEX ops_dispatch_batch_status_9708ff63 ON public.ops_dispatch_batch USING btree (status);
CREATE INDEX ops_dispatch_batch_status_9708ff63_like ON public.ops_dispatch_batch USING btree (status varchar_pattern_ops);
CREATE INDEX ops_driver__driver__de34a4_idx ON public.ops_driver_reminder USING btree (driver_id, status);
CREATE INDEX ops_driver__waybill_408273_idx ON public.ops_driver_reminder USING btree (waybill_id, status);
CREATE INDEX ops_driver__waybill_73834f_idx ON public.ops_driver_checkin USING btree (waybill_id, node);
CREATE INDEX ops_driver_checkin_created_at_9d8aabaa ON public.ops_driver_checkin USING btree (created_at);
CREATE INDEX ops_driver_checkin_driver_id_2273be29 ON public.ops_driver_checkin USING btree (driver_id);
CREATE INDEX ops_driver_checkin_node_89d20fad ON public.ops_driver_checkin USING btree (node);
CREATE INDEX ops_driver_checkin_node_89d20fad_like ON public.ops_driver_checkin USING btree (node varchar_pattern_ops);
CREATE INDEX ops_driver_checkin_waybill_id_a8109c77 ON public.ops_driver_checkin USING btree (waybill_id);
CREATE INDEX ops_driver_reminder_created_at_53aedee4 ON public.ops_driver_reminder USING btree (created_at);
CREATE INDEX ops_driver_reminder_driver_id_06666b96 ON public.ops_driver_reminder USING btree (driver_id);
CREATE INDEX ops_driver_reminder_sent_by_id_f73af4cd ON public.ops_driver_reminder USING btree (sent_by_id);
CREATE INDEX ops_driver_reminder_status_cf99c78e ON public.ops_driver_reminder USING btree (status);
CREATE INDEX ops_driver_reminder_status_cf99c78e_like ON public.ops_driver_reminder USING btree (status varchar_pattern_ops);
CREATE INDEX ops_driver_reminder_template_id_afc1ec4c ON public.ops_driver_reminder USING btree (template_id);
CREATE INDEX ops_driver_reminder_waybill_id_1e765bfb ON public.ops_driver_reminder USING btree (waybill_id);
CREATE INDEX ops_excepti_excepti_537100_idx ON public.ops_exception USING btree (exception_type, status);
CREATE INDEX ops_excepti_excepti_97f11f_idx ON public.ops_exception_event USING btree (exception_id, event_time);
CREATE INDEX ops_excepti_level_d824cc_idx ON public.ops_exception USING btree (level, status);
CREATE INDEX ops_exception_assignee_id_a0cf707f ON public.ops_exception USING btree (assignee_id);
CREATE INDEX ops_exception_created_at_036a4018 ON public.ops_exception USING btree (created_at);
CREATE INDEX ops_exception_event_actor_id_f343024f ON public.ops_exception_event USING btree (actor_id);
CREATE INDEX ops_exception_event_created_at_cf9ea355 ON public.ops_exception_event USING btree (created_at);
CREATE INDEX ops_exception_event_event_time_40176106 ON public.ops_exception_event USING btree (event_time);
CREATE INDEX ops_exception_event_exception_id_f6318919 ON public.ops_exception_event USING btree (exception_id);
CREATE INDEX ops_exception_order_id_a6a6d564 ON public.ops_exception USING btree (order_id);
CREATE INDEX ops_exception_reported_by_id_7c100b40 ON public.ops_exception USING btree (reported_by_id);
CREATE INDEX ops_exception_waybill_id_7603950c ON public.ops_exception USING btree (waybill_id);
CREATE INDEX ops_number_counter_scope_7193ad06_like ON public.ops_number_counter USING btree (scope varchar_pattern_ops);
CREATE INDEX ops_order_ai_conversation_id_c961cbb5 ON public.ops_order USING btree (ai_conversation_id);
CREATE INDEX ops_order_ai_conversation_id_c961cbb5_like ON public.ops_order USING btree (ai_conversation_id varchar_pattern_ops);
CREATE INDEX ops_order_approval_status_8110deb1 ON public.ops_order USING btree (approval_status);
CREATE INDEX ops_order_approval_status_8110deb1_like ON public.ops_order USING btree (approval_status varchar_pattern_ops);
CREATE INDEX ops_order_approved_by_id_5dced77f ON public.ops_order USING btree (approved_by_id);
CREATE INDEX ops_order_assigned_by_id_3a06c5ea ON public.ops_order USING btree (assigned_by_id);
CREATE INDEX ops_order_assigned_to_id_0a982534 ON public.ops_order USING btree (assigned_to_id);
CREATE INDEX ops_order_attachment_created_at_a69b899a ON public.ops_order_attachment USING btree (created_at);
CREATE INDEX ops_order_attachment_order_id_78bc58ee ON public.ops_order_attachment USING btree (order_id);
CREATE INDEX ops_order_attachment_uploaded_by_id_80bae8d0 ON public.ops_order_attachment USING btree (uploaded_by_id);
CREATE INDEX ops_order_cargo_item_created_at_f2a04458 ON public.ops_order_cargo_item USING btree (created_at);
CREATE INDEX ops_order_cargo_item_order_id_2d75e3d0 ON public.ops_order_cargo_item USING btree (order_id);
CREATE INDEX ops_order_channel_d7e40bf1 ON public.ops_order USING btree (channel);
CREATE INDEX ops_order_channel_d7e40bf1_like ON public.ops_order USING btree (channel varchar_pattern_ops);
CREATE INDEX ops_order_channel_fa436f_idx ON public.ops_order USING btree (channel, status);
CREATE INDEX ops_order_claimed_1eb254_idx ON public.ops_order USING btree (claimed_by_id, status);
CREATE INDEX ops_order_claimed_by_id_c6a0b730 ON public.ops_order USING btree (claimed_by_id);
CREATE INDEX ops_order_created_976dff_idx ON public.ops_order USING btree (created_by_id, status);
CREATE INDEX ops_order_created_at_46aac10b ON public.ops_order USING btree (created_at);
CREATE INDEX ops_order_created_by_id_6d4c20b7 ON public.ops_order USING btree (created_by_id);
CREATE INDEX ops_order_customer_id_c6f3619c ON public.ops_order USING btree (customer_id);
CREATE INDEX ops_order_e_order_i_a03619_idx ON public.ops_order_event USING btree (order_id, event_time);
CREATE INDEX ops_order_event_actor_id_a2c700e4 ON public.ops_order_event USING btree (actor_id);
CREATE INDEX ops_order_event_created_at_e9e73364 ON public.ops_order_event USING btree (created_at);
CREATE INDEX ops_order_event_event_time_d682cd85 ON public.ops_order_event USING btree (event_time);
CREATE INDEX ops_order_event_order_id_d1435c67 ON public.ops_order_event USING btree (order_id);
CREATE INDEX ops_order_is_deleted_e386222f ON public.ops_order USING btree (is_deleted);
CREATE INDEX ops_order_order_no_18f092cb_like ON public.ops_order USING btree (order_no varchar_pattern_ops);
CREATE INDEX ops_order_sla_status_03ed76dd ON public.ops_order USING btree (sla_status);
CREATE INDEX ops_order_sla_status_03ed76dd_like ON public.ops_order USING btree (sla_status varchar_pattern_ops);
CREATE INDEX ops_order_status_1209e9_idx ON public.ops_order USING btree (status, priority);
CREATE INDEX ops_order_status_21f696_idx ON public.ops_order USING btree (status, created_at DESC);
CREATE INDEX ops_order_status_4f9fa737 ON public.ops_order USING btree (status);
CREATE INDEX ops_order_status_4f9fa737_like ON public.ops_order USING btree (status varchar_pattern_ops);
CREATE INDEX ops_order_stop_created_at_9ac97632 ON public.ops_order_stop USING btree (created_at);
CREATE INDEX ops_order_stop_order_id_021e750d ON public.ops_order_stop USING btree (order_id);
CREATE INDEX ops_order_template_created_at_1ca50d49 ON public.ops_order_template USING btree (created_at);
CREATE INDEX ops_order_template_created_by_id_dd0013b3 ON public.ops_order_template USING btree (created_by_id);
CREATE INDEX ops_order_template_is_deleted_8adca001 ON public.ops_order_template USING btree (is_deleted);
CREATE INDEX ops_receipt_created_at_419c54dc ON public.ops_receipt USING btree (created_at);
CREATE INDEX ops_receipt_ocr_sta_9f0df2_idx ON public.ops_receipt USING btree (ocr_status);
CREATE INDEX ops_receipt_uploaded_by_id_907181a8 ON public.ops_receipt USING btree (uploaded_by_id);
CREATE INDEX ops_receipt_waybill_50f53e_idx ON public.ops_receipt USING btree (waybill_id, status);
CREATE INDEX ops_receipt_waybill_id_3b94c8c5 ON public.ops_receipt USING btree (waybill_id);
CREATE INDEX ops_reminder_template_created_at_c421fbf4 ON public.ops_reminder_template USING btree (created_at);
CREATE INDEX ops_reminder_template_created_by_id_b9945f13 ON public.ops_reminder_template USING btree (created_by_id);
CREATE INDEX ops_reminder_template_is_deleted_b8c0edce ON public.ops_reminder_template USING btree (is_deleted);
CREATE INDEX ops_trackin_waybill_bc72ea_idx ON public.ops_tracking_point USING btree (waybill_id, reported_at);
CREATE INDEX ops_tracking_point_created_at_cebca2e4 ON public.ops_tracking_point USING btree (created_at);
CREATE INDEX ops_tracking_point_waybill_id_9aa6496f ON public.ops_tracking_point USING btree (waybill_id);
CREATE INDEX ops_waybill_ai_conversation_id_89a700ca ON public.ops_waybill USING btree (ai_conversation_id);
CREATE INDEX ops_waybill_ai_conversation_id_89a700ca_like ON public.ops_waybill USING btree (ai_conversation_id varchar_pattern_ops);
CREATE INDEX ops_waybill_batch_id_87aaecb7 ON public.ops_waybill USING btree (batch_id);
CREATE INDEX ops_waybill_carrier_id_bd362e32 ON public.ops_waybill USING btree (carrier_id);
CREATE INDEX ops_waybill_created_at_f13681df ON public.ops_waybill USING btree (created_at);
CREATE INDEX ops_waybill_custome_9a905a_idx ON public.ops_waybill USING btree (customer_id, status);
CREATE INDEX ops_waybill_customer_id_da700e60 ON public.ops_waybill USING btree (customer_id);
CREATE INDEX ops_waybill_driver__37467a_idx ON public.ops_waybill USING btree (driver_id, status);
CREATE INDEX ops_waybill_driver_created_at_8bc60046 ON public.ops_waybill_driver USING btree (created_at);
CREATE INDEX ops_waybill_driver_driver_id_903a574c ON public.ops_waybill_driver USING btree (driver_id);
CREATE INDEX ops_waybill_driver_id_8e1cc509 ON public.ops_waybill USING btree (driver_id);
CREATE INDEX ops_waybill_driver_role_07da5e52 ON public.ops_waybill_driver USING btree (role);
CREATE INDEX ops_waybill_driver_role_07da5e52_like ON public.ops_waybill_driver USING btree (role varchar_pattern_ops);
CREATE INDEX ops_waybill_driver_waybill_id_15ca233b ON public.ops_waybill_driver USING btree (waybill_id);
CREATE INDEX ops_waybill_eta_dri_ba2c68_idx ON public.ops_waybill USING btree (eta_drift_minutes DESC);
CREATE INDEX ops_waybill_event_created_at_290930ff ON public.ops_waybill_event USING btree (created_at);
CREATE INDEX ops_waybill_event_t_7333da_idx ON public.ops_waybill_event USING btree (event_type, event_time);
CREATE INDEX ops_waybill_event_waybill_id_92cb7f12 ON public.ops_waybill_event USING btree (waybill_id);
CREATE INDEX ops_waybill_order_id_29238a38 ON public.ops_waybill USING btree (order_id);
CREATE INDEX ops_waybill_organization_id_15048c4e ON public.ops_waybill USING btree (organization_id);
CREATE INDEX ops_waybill_parent_id_262b8fbb ON public.ops_waybill USING btree (parent_id);
CREATE INDEX ops_waybill_planned_route_id_0489fed3 ON public.ops_waybill USING btree (planned_route_id);
CREATE INDEX ops_waybill_project_idx ON public.ops_waybill USING btree (project_id);
CREATE INDEX ops_waybill_receipt_6f94a1_idx ON public.ops_waybill USING btree (receipt_status);
CREATE INDEX ops_waybill_status_3d42b9_idx ON public.ops_waybill USING btree (status, risk_level);
CREATE INDEX ops_waybill_status_4d8c84_idx ON public.ops_waybill USING btree (status, created_at DESC);
CREATE INDEX ops_waybill_stop_created_at_90b6124e ON public.ops_waybill_stop USING btree (created_at);
CREATE INDEX ops_waybill_stop_status_602db2f8 ON public.ops_waybill_stop USING btree (status);
CREATE INDEX ops_waybill_stop_status_602db2f8_like ON public.ops_waybill_stop USING btree (status varchar_pattern_ops);
CREATE INDEX ops_waybill_stop_waybill_id_b2f9284e ON public.ops_waybill_stop USING btree (waybill_id);
CREATE INDEX ops_waybill_trailer_id_d37db4b3 ON public.ops_waybill USING btree (trailer_id);
CREATE INDEX ops_waybill_vehicle_id_109da1b8 ON public.ops_waybill USING btree (vehicle_id);
CREATE INDEX ops_waybill_waybill_8e4f34_idx ON public.ops_waybill_event USING btree (waybill_id, event_time);
CREATE INDEX ops_waybill_waybill_ca40c6_idx ON public.ops_waybill_stop USING btree (waybill_id, seq);
CREATE INDEX ops_waybill_waybill_no_7b54c42c_like ON public.ops_waybill USING btree (waybill_no varchar_pattern_ops);
CREATE INDEX tel_alert_alert_t_cefcb1_idx ON public.tel_alert USING btree (alert_type, status);
CREATE INDEX tel_alert_created_at_63ca7c76 ON public.tel_alert USING btree (created_at);
CREATE INDEX tel_alert_device_id_d6411cb1 ON public.tel_alert USING btree (device_id);
CREATE INDEX tel_alert_handled_by_id_a52ef68d ON public.tel_alert USING btree (handled_by_id);
CREATE INDEX tel_alert_vehicle_5ffdf1_idx ON public.tel_alert USING btree (vehicle_id, status);
CREATE INDEX tel_alert_vehicle_id_cce08708 ON public.tel_alert USING btree (vehicle_id);
CREATE INDEX tel_alert_waybill_id_ac37bb12 ON public.tel_alert USING btree (waybill_id);
CREATE INDEX tel_device_created_at_5c328d39 ON public.tel_device USING btree (created_at);
CREATE INDEX tel_device_device__57ec41_idx ON public.tel_device USING btree (device_type, status);
CREATE INDEX tel_device_device_no_347e61eb_like ON public.tel_device USING btree (device_no varchar_pattern_ops);
CREATE INDEX tel_device_vehicle_id_86dca56a ON public.tel_device USING btree (vehicle_id);
CREATE INDEX tel_geofence_created_at_5c6a5cf6 ON public.tel_geofence USING btree (created_at);
CREATE INDEX tel_geofence_is_active_c75ae5d4 ON public.tel_geofence USING btree (is_active);
CREATE INDEX tel_geofence_state_created_at_42b9dff3 ON public.tel_geofence_state USING btree (created_at);
CREATE INDEX tel_geofence_state_geofence_id_242975bf ON public.tel_geofence_state USING btree (geofence_id);
CREATE INDEX tel_geofence_state_vehicle_id_24830845 ON public.tel_geofence_state USING btree (vehicle_id);
CREATE INDEX tel_vehicle_state_created_at_96498a15 ON public.tel_vehicle_state USING btree (created_at);
CREATE INDEX tel_vehicle_state_online_6b333d29 ON public.tel_vehicle_state USING btree (online);
CREATE INDEX tel_vehicle_state_waybill_id_d24994f3 ON public.tel_vehicle_state USING btree (waybill_id);
ALTER TABLE ONLY public.accounts_user
    ADD CONSTRAINT accounts_user_organization_id_d808f5ca_fk_iam_organization_id FOREIGN KEY (organization_id) REFERENCES public.iam_organization(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.ai_agent_suggestion
    ADD CONSTRAINT ai_agent_suggestion_confirmed_by_id_5bdc4ec0_fk_accounts_ FOREIGN KEY (confirmed_by_id) REFERENCES public.accounts_user(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.ai_agent_suggestion
    ADD CONSTRAINT ai_agent_suggestion_waybill_id_fe46fe12_fk_ops_waybill_id FOREIGN KEY (waybill_id) REFERENCES public.ops_waybill(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.audit_log
    ADD CONSTRAINT audit_log_actor_id_dfa07704_fk_accounts_user_id FOREIGN KEY (actor_id) REFERENCES public.accounts_user(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.fin_expense_record
    ADD CONSTRAINT fin_expense_record_waybill_id_1b06c61b_fk_ops_waybill_id FOREIGN KEY (waybill_id) REFERENCES public.ops_waybill(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.fin_payment_request
    ADD CONSTRAINT fin_payment_request_waybill_id_148a8045_fk_ops_waybill_id FOREIGN KEY (waybill_id) REFERENCES public.ops_waybill(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.fin_pricing_rule
    ADD CONSTRAINT fin_pricing_rule_carrier_id_69055060_fk_md_carrier_id FOREIGN KEY (carrier_id) REFERENCES public.md_carrier(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.fin_pricing_rule
    ADD CONSTRAINT fin_pricing_rule_customer_id_c65ddc40_fk_md_customer_id FOREIGN KEY (customer_id) REFERENCES public.md_customer(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.fin_reimbursement
    ADD CONSTRAINT fin_reimbursement_approved_by_id_2d125c06_fk_accounts_user_id FOREIGN KEY (approved_by_id) REFERENCES public.accounts_user(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.fin_reimbursement
    ADD CONSTRAINT fin_reimbursement_payment_request_id_a37eef7a_fk_fin_payme FOREIGN KEY (payment_request_id) REFERENCES public.fin_payment_request(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.fin_reimbursement
    ADD CONSTRAINT fin_reimbursement_submitted_by_id_139934a1_fk_accounts_user_id FOREIGN KEY (submitted_by_id) REFERENCES public.accounts_user(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.fin_reimbursement
    ADD CONSTRAINT fin_reimbursement_waybill_id_1d285669_fk_ops_waybill_id FOREIGN KEY (waybill_id) REFERENCES public.ops_waybill(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.fin_statement
    ADD CONSTRAINT fin_statement_confirmed_by_id_5f724e70_fk_accounts_user_id FOREIGN KEY (confirmed_by_id) REFERENCES public.accounts_user(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.fin_statement_line
    ADD CONSTRAINT fin_statement_line_expense_record_id_fabfd20c_fk_fin_expen FOREIGN KEY (expense_record_id) REFERENCES public.fin_expense_record(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.fin_statement_line
    ADD CONSTRAINT fin_statement_line_statement_id_d115bccc_fk_fin_statement_id FOREIGN KEY (statement_id) REFERENCES public.fin_statement(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.fin_statement_payment
    ADD CONSTRAINT fin_statement_paymen_created_by_id_8debb014_fk_accounts_ FOREIGN KEY (created_by_id) REFERENCES public.accounts_user(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.fin_statement_payment
    ADD CONSTRAINT fin_statement_payment_statement_id_562b6b92_fk_fin_statement_id FOREIGN KEY (statement_id) REFERENCES public.fin_statement(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.fin_webhook_delivery
    ADD CONSTRAINT fin_webhook_delivery_webhook_id_1f594cf2_fk_fin_webhook_id FOREIGN KEY (webhook_id) REFERENCES public.fin_webhook(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.iam_account_handover
    ADD CONSTRAINT iam_account_handover_from_employee_id_22dcddf8_fk_iam_emplo FOREIGN KEY (from_employee_id) REFERENCES public.iam_employee(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.iam_account_handover
    ADD CONSTRAINT iam_account_handover_operator_id_3633570b_fk_accounts_user_id FOREIGN KEY (operator_id) REFERENCES public.accounts_user(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.iam_account_handover
    ADD CONSTRAINT iam_account_handover_to_employee_id_de92dbd2_fk_iam_employee_id FOREIGN KEY (to_employee_id) REFERENCES public.iam_employee(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.iam_api_key
    ADD CONSTRAINT iam_api_key_organization_id_7e6f7f98_fk_iam_organization_id FOREIGN KEY (organization_id) REFERENCES public.iam_organization(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.iam_department
    ADD CONSTRAINT iam_department_manager_id_dc3369cb_fk_iam_employee_id FOREIGN KEY (manager_id) REFERENCES public.iam_employee(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.iam_department
    ADD CONSTRAINT iam_department_organization_id_8ff6605f_fk_iam_organization_id FOREIGN KEY (organization_id) REFERENCES public.iam_organization(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.iam_department
    ADD CONSTRAINT iam_department_parent_id_6e292384_fk_iam_department_id FOREIGN KEY (parent_id) REFERENCES public.iam_department(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.iam_employee
    ADD CONSTRAINT iam_employee_department_id_5f79239f_fk_iam_department_id FOREIGN KEY (department_id) REFERENCES public.iam_department(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.iam_employee_group_roles
    ADD CONSTRAINT iam_employee_group_r_employeegroup_id_b4f326a5_fk_iam_emplo FOREIGN KEY (employeegroup_id) REFERENCES public.iam_employee_group(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.iam_employee_group_roles
    ADD CONSTRAINT iam_employee_group_roles_role_id_33b873e7_fk_iam_role_id FOREIGN KEY (role_id) REFERENCES public.iam_role(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.iam_employee_groups
    ADD CONSTRAINT iam_employee_groups_employee_id_38929552_fk_iam_employee_id FOREIGN KEY (employee_id) REFERENCES public.iam_employee(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.iam_employee_groups
    ADD CONSTRAINT iam_employee_groups_employeegroup_id_7a9003d2_fk_iam_emplo FOREIGN KEY (employeegroup_id) REFERENCES public.iam_employee_group(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.iam_employee
    ADD CONSTRAINT iam_employee_organization_id_55536134_fk_iam_organization_id FOREIGN KEY (organization_id) REFERENCES public.iam_organization(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.iam_employee
    ADD CONSTRAINT iam_employee_supervisor_id_a5b0f24f_fk_iam_employee_id FOREIGN KEY (supervisor_id) REFERENCES public.iam_employee(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.iam_employee
    ADD CONSTRAINT iam_employee_user_id_96ee1f36_fk_accounts_user_id FOREIGN KEY (user_id) REFERENCES public.accounts_user(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.iam_login_attempt
    ADD CONSTRAINT iam_login_attempt_user_id_57aa6b8e_fk_accounts_user_id FOREIGN KEY (user_id) REFERENCES public.accounts_user(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.iam_organization
    ADD CONSTRAINT iam_organization_parent_id_6316596a_fk_iam_organization_id FOREIGN KEY (parent_id) REFERENCES public.iam_organization(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.iam_role_assignment
    ADD CONSTRAINT iam_role_assignment_organization_id_1cf5de14_fk_iam_organ FOREIGN KEY (organization_id) REFERENCES public.iam_organization(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.iam_role_assignment
    ADD CONSTRAINT iam_role_assignment_role_id_b9205b1c_fk_iam_role_id FOREIGN KEY (role_id) REFERENCES public.iam_role(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.iam_role_assignment
    ADD CONSTRAINT iam_role_assignment_user_id_18f31edd_fk_accounts_user_id FOREIGN KEY (user_id) REFERENCES public.accounts_user(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.iam_role_permissions
    ADD CONSTRAINT iam_role_permissions_permission_id_8409a90e_fk_iam_permi FOREIGN KEY (permission_id) REFERENCES public.iam_permission(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.iam_role_permissions
    ADD CONSTRAINT iam_role_permissions_role_id_5c73a598_fk_iam_role_id FOREIGN KEY (role_id) REFERENCES public.iam_role(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.iam_service_area
    ADD CONSTRAINT iam_service_area_organization_id_b4835aaf_fk_iam_organ FOREIGN KEY (organization_id) REFERENCES public.iam_organization(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.md_carrier_lane_price
    ADD CONSTRAINT md_carrier_lane_price_carrier_id_159f0af9_fk_md_carrier_id FOREIGN KEY (carrier_id) REFERENCES public.md_carrier(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.md_driver
    ADD CONSTRAINT md_driver_carrier_id_a9f18029_fk_md_carrier_id FOREIGN KEY (carrier_id) REFERENCES public.md_carrier(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.md_driver_credential
    ADD CONSTRAINT md_driver_credential_driver_id_98614c03_fk_md_driver_id FOREIGN KEY (driver_id) REFERENCES public.md_driver(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.md_driver_credential
    ADD CONSTRAINT md_driver_credential_uploaded_by_id_bce43bf9_fk_accounts_ FOREIGN KEY (uploaded_by_id) REFERENCES public.accounts_user(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.md_vehicle
    ADD CONSTRAINT md_vehicle_carrier_id_9902574d_fk_md_carrier_id FOREIGN KEY (carrier_id) REFERENCES public.md_carrier(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.ntf_notification
    ADD CONSTRAINT ntf_notification_recipient_id_539bed50_fk_accounts_user_id FOREIGN KEY (recipient_id) REFERENCES public.accounts_user(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.ops_contract
    ADD CONSTRAINT ops_contract_driver_id_753d8137_fk_md_driver_id FOREIGN KEY (driver_id) REFERENCES public.md_driver(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.ops_contract
    ADD CONSTRAINT ops_contract_waybill_id_ffe3e83c_fk_ops_waybill_id FOREIGN KEY (waybill_id) REFERENCES public.ops_waybill(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.ops_dispatch_batch
    ADD CONSTRAINT ops_dispatch_batch_carrier_id_47dec5b2_fk_md_carrier_id FOREIGN KEY (carrier_id) REFERENCES public.md_carrier(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.ops_dispatch_batch
    ADD CONSTRAINT ops_dispatch_batch_created_by_id_8981a4b4_fk_accounts_user_id FOREIGN KEY (created_by_id) REFERENCES public.accounts_user(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.ops_dispatch_batch
    ADD CONSTRAINT ops_dispatch_batch_organization_id_fca372ae_fk_iam_organ FOREIGN KEY (organization_id) REFERENCES public.iam_organization(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.ops_driver_checkin
    ADD CONSTRAINT ops_driver_checkin_driver_id_2273be29_fk_md_driver_id FOREIGN KEY (driver_id) REFERENCES public.md_driver(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.ops_driver_checkin
    ADD CONSTRAINT ops_driver_checkin_waybill_id_a8109c77_fk_ops_waybill_id FOREIGN KEY (waybill_id) REFERENCES public.ops_waybill(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.ops_driver_reminder
    ADD CONSTRAINT ops_driver_reminder_driver_id_06666b96_fk_md_driver_id FOREIGN KEY (driver_id) REFERENCES public.md_driver(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.ops_driver_reminder
    ADD CONSTRAINT ops_driver_reminder_sent_by_id_f73af4cd_fk_accounts_user_id FOREIGN KEY (sent_by_id) REFERENCES public.accounts_user(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.ops_driver_reminder
    ADD CONSTRAINT ops_driver_reminder_template_id_afc1ec4c_fk_ops_remin FOREIGN KEY (template_id) REFERENCES public.ops_reminder_template(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.ops_driver_reminder
    ADD CONSTRAINT ops_driver_reminder_waybill_id_1e765bfb_fk_ops_waybill_id FOREIGN KEY (waybill_id) REFERENCES public.ops_waybill(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.ops_exception
    ADD CONSTRAINT ops_exception_assignee_id_a0cf707f_fk_accounts_user_id FOREIGN KEY (assignee_id) REFERENCES public.accounts_user(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.ops_exception_event
    ADD CONSTRAINT ops_exception_event_actor_id_f343024f_fk_accounts_user_id FOREIGN KEY (actor_id) REFERENCES public.accounts_user(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.ops_exception_event
    ADD CONSTRAINT ops_exception_event_exception_id_f6318919_fk_ops_exception_id FOREIGN KEY (exception_id) REFERENCES public.ops_exception(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.ops_exception
    ADD CONSTRAINT ops_exception_order_id_a6a6d564_fk_ops_order_id FOREIGN KEY (order_id) REFERENCES public.ops_order(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.ops_exception
    ADD CONSTRAINT ops_exception_reported_by_id_7c100b40_fk_accounts_user_id FOREIGN KEY (reported_by_id) REFERENCES public.accounts_user(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.ops_exception
    ADD CONSTRAINT ops_exception_waybill_id_7603950c_fk_ops_waybill_id FOREIGN KEY (waybill_id) REFERENCES public.ops_waybill(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.ops_order
    ADD CONSTRAINT ops_order_approved_by_id_5dced77f_fk_accounts_user_id FOREIGN KEY (approved_by_id) REFERENCES public.accounts_user(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.ops_order
    ADD CONSTRAINT ops_order_assigned_by_id_3a06c5ea_fk_accounts_user_id FOREIGN KEY (assigned_by_id) REFERENCES public.accounts_user(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.ops_order
    ADD CONSTRAINT ops_order_assigned_to_id_0a982534_fk_accounts_user_id FOREIGN KEY (assigned_to_id) REFERENCES public.accounts_user(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.ops_order_attachment
    ADD CONSTRAINT ops_order_attachment_order_id_78bc58ee_fk_ops_order_id FOREIGN KEY (order_id) REFERENCES public.ops_order(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.ops_order_attachment
    ADD CONSTRAINT ops_order_attachment_uploaded_by_id_80bae8d0_fk_accounts_ FOREIGN KEY (uploaded_by_id) REFERENCES public.accounts_user(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.ops_order_cargo_item
    ADD CONSTRAINT ops_order_cargo_item_order_id_2d75e3d0_fk_ops_order_id FOREIGN KEY (order_id) REFERENCES public.ops_order(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.ops_order
    ADD CONSTRAINT ops_order_claimed_by_id_c6a0b730_fk_accounts_user_id FOREIGN KEY (claimed_by_id) REFERENCES public.accounts_user(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.ops_order
    ADD CONSTRAINT ops_order_created_by_id_6d4c20b7_fk_accounts_user_id FOREIGN KEY (created_by_id) REFERENCES public.accounts_user(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.ops_order
    ADD CONSTRAINT ops_order_customer_id_c6f3619c_fk_md_customer_id FOREIGN KEY (customer_id) REFERENCES public.md_customer(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.ops_order_event
    ADD CONSTRAINT ops_order_event_actor_id_a2c700e4_fk_accounts_user_id FOREIGN KEY (actor_id) REFERENCES public.accounts_user(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.ops_order_event
    ADD CONSTRAINT ops_order_event_order_id_d1435c67_fk_ops_order_id FOREIGN KEY (order_id) REFERENCES public.ops_order(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.ops_order_stop
    ADD CONSTRAINT ops_order_stop_order_id_021e750d_fk_ops_order_id FOREIGN KEY (order_id) REFERENCES public.ops_order(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.ops_order_template
    ADD CONSTRAINT ops_order_template_created_by_id_dd0013b3_fk_accounts_user_id FOREIGN KEY (created_by_id) REFERENCES public.accounts_user(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.ops_receipt
    ADD CONSTRAINT ops_receipt_uploaded_by_id_907181a8_fk_accounts_user_id FOREIGN KEY (uploaded_by_id) REFERENCES public.accounts_user(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.ops_receipt
    ADD CONSTRAINT ops_receipt_waybill_id_3b94c8c5_fk_ops_waybill_id FOREIGN KEY (waybill_id) REFERENCES public.ops_waybill(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.ops_reminder_template
    ADD CONSTRAINT ops_reminder_templat_created_by_id_b9945f13_fk_accounts_ FOREIGN KEY (created_by_id) REFERENCES public.accounts_user(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.ops_tracking_point
    ADD CONSTRAINT ops_tracking_point_waybill_id_9aa6496f_fk_ops_waybill_id FOREIGN KEY (waybill_id) REFERENCES public.ops_waybill(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.ops_waybill
    ADD CONSTRAINT ops_waybill_batch_id_87aaecb7_fk_ops_dispatch_batch_id FOREIGN KEY (batch_id) REFERENCES public.ops_dispatch_batch(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.ops_waybill
    ADD CONSTRAINT ops_waybill_carrier_id_bd362e32_fk_md_carrier_id FOREIGN KEY (carrier_id) REFERENCES public.md_carrier(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.ops_waybill
    ADD CONSTRAINT ops_waybill_customer_id_da700e60_fk_md_customer_id FOREIGN KEY (customer_id) REFERENCES public.md_customer(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.ops_waybill_driver
    ADD CONSTRAINT ops_waybill_driver_driver_id_903a574c_fk_md_driver_id FOREIGN KEY (driver_id) REFERENCES public.md_driver(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.ops_waybill
    ADD CONSTRAINT ops_waybill_driver_id_8e1cc509_fk_md_driver_id FOREIGN KEY (driver_id) REFERENCES public.md_driver(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.ops_waybill_driver
    ADD CONSTRAINT ops_waybill_driver_waybill_id_15ca233b_fk_ops_waybill_id FOREIGN KEY (waybill_id) REFERENCES public.ops_waybill(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.ops_waybill_event
    ADD CONSTRAINT ops_waybill_event_waybill_id_92cb7f12_fk_ops_waybill_id FOREIGN KEY (waybill_id) REFERENCES public.ops_waybill(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.ops_waybill
    ADD CONSTRAINT ops_waybill_order_id_29238a38_fk_ops_order_id FOREIGN KEY (order_id) REFERENCES public.ops_order(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.ops_waybill
    ADD CONSTRAINT ops_waybill_organization_id_15048c4e_fk_iam_organization_id FOREIGN KEY (organization_id) REFERENCES public.iam_organization(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.ops_waybill
    ADD CONSTRAINT ops_waybill_parent_id_262b8fbb_fk_ops_waybill_id FOREIGN KEY (parent_id) REFERENCES public.ops_waybill(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.ops_waybill
    ADD CONSTRAINT ops_waybill_planned_route_id_0489fed3_fk_md_route_id FOREIGN KEY (planned_route_id) REFERENCES public.md_route(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.ops_waybill_stop
    ADD CONSTRAINT ops_waybill_stop_waybill_id_b2f9284e_fk_ops_waybill_id FOREIGN KEY (waybill_id) REFERENCES public.ops_waybill(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.ops_waybill
    ADD CONSTRAINT ops_waybill_trailer_id_d37db4b3_fk_md_vehicle_id FOREIGN KEY (trailer_id) REFERENCES public.md_vehicle(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.ops_waybill
    ADD CONSTRAINT ops_waybill_vehicle_id_109da1b8_fk_md_vehicle_id FOREIGN KEY (vehicle_id) REFERENCES public.md_vehicle(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.tel_alert
    ADD CONSTRAINT tel_alert_device_id_d6411cb1_fk_tel_device_id FOREIGN KEY (device_id) REFERENCES public.tel_device(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.tel_alert
    ADD CONSTRAINT tel_alert_handled_by_id_a52ef68d_fk_accounts_user_id FOREIGN KEY (handled_by_id) REFERENCES public.accounts_user(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.tel_alert
    ADD CONSTRAINT tel_alert_vehicle_id_cce08708_fk_md_vehicle_id FOREIGN KEY (vehicle_id) REFERENCES public.md_vehicle(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.tel_alert
    ADD CONSTRAINT tel_alert_waybill_id_ac37bb12_fk_ops_waybill_id FOREIGN KEY (waybill_id) REFERENCES public.ops_waybill(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.tel_device
    ADD CONSTRAINT tel_device_vehicle_id_86dca56a_fk_md_vehicle_id FOREIGN KEY (vehicle_id) REFERENCES public.md_vehicle(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.tel_geofence_state
    ADD CONSTRAINT tel_geofence_state_geofence_id_242975bf_fk_tel_geofence_id FOREIGN KEY (geofence_id) REFERENCES public.tel_geofence(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.tel_geofence_state
    ADD CONSTRAINT tel_geofence_state_vehicle_id_24830845_fk_md_vehicle_id FOREIGN KEY (vehicle_id) REFERENCES public.md_vehicle(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.tel_vehicle_state
    ADD CONSTRAINT tel_vehicle_state_vehicle_id_aa181608_fk_md_vehicle_id FOREIGN KEY (vehicle_id) REFERENCES public.md_vehicle(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.tel_vehicle_state
    ADD CONSTRAINT tel_vehicle_state_waybill_id_d24994f3_fk_ops_waybill_id FOREIGN KEY (waybill_id) REFERENCES public.ops_waybill(id) DEFERRABLE INITIALLY DEFERRED;
