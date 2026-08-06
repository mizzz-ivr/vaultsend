export type AuthUser = {
  id: string;
  email: string;
  display_name?: string;
  status: string;
  email_verified_at?: string;
  created_at: string;
};

export type AuthResponse = {
  user: AuthUser;
};

export type ShipmentListItem = {
  id: string;
  subject: string;
  share_mode: "url_shared" | "recipient_restricted" | string;
  status: string;
  created_at: string;
  expires_at: string;
  download_count: number;
  max_download_count: number;
  file_count: number;
};

export type ShipmentListResponse = {
  items: ShipmentListItem[];
  limit: number;
  offset: number;
  total: number;
};

export type ShipmentFile = {
  id: string;
  file_name: string;
  size: number;
};

export type ShipmentRecipient = {
  id: string;
  email: string;
  status: string;
};

export type ShipmentRecipientSummary = {
  recipient_id: string;
  email: string;
  recipient_status: string;
  notification_count: number;
  last_notification_status?: string;
  last_notification_type?: string;
  last_notified_at?: string;
  first_download_at?: string;
  last_download_at?: string;
  download_count: number;
  has_downloaded: boolean;
};

export type ShipmentDetail = {
  id: string;
  status: string;
  share_mode: string;
  subject: string;
  message?: string;
  expires_at: string;
  max_download_count: number;
  download_count: number;
  last_download_at?: string;
  files: ShipmentFile[];
  recipients: ShipmentRecipient[];
  notification_summary: {
    total_notifications: number;
    queued_count: number;
    sent_count: number;
    failed_count: number;
    last_notification_at?: string;
  };
  recipient_summaries: ShipmentRecipientSummary[];
};

export type PresignedUploadPart = {
  part_number: number;
  presigned_url: string;
};

export type CreateUploadResponse = {
  upload_session_id: string;
  shipment_id: string;
  object_key: string;
  s3_upload_id: string;
  part_size: number;
  parts: PresignedUploadPart[];
  expires_at: string;
};

export type CompleteUploadResponse = {
  upload_session_id: string;
  file_id: string;
  shipment_id: string;
  status: string;
};

export type CreateShipmentResponse = {
  id: string;
  status: string;
  share_mode: string;
  expires_at: string;
  max_download_count: number;
  access_url?: string;
  recipients: Array<{
    id: string;
    email: string;
    status: string;
  }>;
  files: Array<{
    id: string;
    original_name: string;
    size_bytes: number;
  }>;
};

export type AccessFile = {
  id: string;
  original_name: string;
  size_bytes: number;
};

export type AccessInspectResponse = {
  requires_password: boolean;
  verified: boolean;
  shipment: {
    id: string;
    share_mode: "url_shared" | "recipient_restricted" | string;
    subject: string;
    message?: string;
    expires_at: string;
    max_download_count: number;
  };
  files: AccessFile[];
};

export type AccessVerifyResponse = {
  granted: boolean;
  expires_at?: string;
};

export type DownloadURLResponse = {
  url: string;
  expires_at: string;
};

export type OrganizationRole = "owner" | "admin" | "member";
export type InvitationRole = "admin" | "member";
export type InvitationStatus = "pending" | "accepted" | "revoked" | "expired";

export type Organization = {
  id: string;
  name: string;
  owner_user_id: string;
};

export type OrganizationListResponse = {
  items: Organization[];
};

export type OrganizationMember = {
  user_id: string;
  role: OrganizationRole;
};

export type OrganizationDetailResponse = {
  organization: Organization;
  members: OrganizationMember[];
};

export type OrganizationBillingDetails = {
  plan: string;
  status: string;
  usage: {
    current_month_shipments: number;
    current_storage_bytes: number;
  };
  members_count: number;
  seat_limit: number;
  current_seat_usage: number;
  remaining_seats: number;
  next_billing_at?: string;
  remaining: {
    remaining_shipments?: number;
  };
};

export type InvoiceSummary = {
  invoice_id: string;
  amount: number;
  currency: string;
  status: string;
  hosted_invoice_url?: string;
  invoice_pdf?: string;
  created_at: string;
  paid_at?: string;
};

export type OrganizationInvoiceListResponse = {
  invoices: InvoiceSummary[];
  has_more: boolean;
  next_starting_after?: string;
};

export type CheckoutResponse = {
  session_id: string;
  url: string;
};

export type OrganizationInvitation = {
  id: string;
  organization_id: string;
  email: string;
  role: InvitationRole;
  status: InvitationStatus;
  expires_at: string;
  last_sent_at?: string;
  accepted_at?: string;
  created_at: string;
};

export type OrganizationInvitationListResponse = {
  items: OrganizationInvitation[];
};

export type OrganizationInvitationInspectResponse = {
  organization_id: string;
  organization_name: string;
  email_masked: string;
  role: InvitationRole;
  status: InvitationStatus;
  expires_at: string;
};

export type OrganizationInvitationAcceptResponse = {
  organization: Organization;
  member: OrganizationMember;
  already_accepted: boolean;
};

export type ApiErrorPayload = {
  error?: string;
  code?: string;
  message?: string;
  request_id?: string;
  upgrade_required?: boolean;
  upgrade_url?: string;
  recommended_plan?: string;
};
