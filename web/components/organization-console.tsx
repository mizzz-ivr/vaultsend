"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import { api, ApiClientError } from "@/lib/api";
import type {
  AuthUser,
  InvoiceSummary,
  Organization,
  OrganizationBillingDetails,
  OrganizationDetailResponse,
  OrganizationInvoiceListResponse,
  OrganizationRole,
} from "@/lib/types";
import styles from "./organization-console.module.css";

const INVOICE_PAGE_SIZE = 10;
const roleLabels: Record<OrganizationRole, string> = {
  owner: "オーナー",
  admin: "管理者",
  member: "メンバー",
};

const dateFormatter = new Intl.DateTimeFormat("ja-JP", {
  year: "numeric",
  month: "short",
  day: "numeric",
});

const numberFormatter = new Intl.NumberFormat("ja-JP");

function formatDate(value?: string) {
  if (!value) return "—";
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? "—" : dateFormatter.format(date);
}

function formatBytes(value: number) {
  if (!Number.isFinite(value) || value < 0) return "—";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let size = value;
  let unitIndex = 0;
  while (size >= 1024 && unitIndex < units.length - 1) {
    size /= 1024;
    unitIndex += 1;
  }
  const digits = unitIndex === 0 || size >= 10 ? 0 : 1;
  return `${size.toFixed(digits)} ${units[unitIndex]}`;
}

function formatMoney(amount: number, currency: string) {
  const normalizedCurrency = currency.trim().toUpperCase() || "JPY";
  const zeroDecimalCurrencies = new Set(["BIF", "CLP", "DJF", "GNF", "JPY", "KMF", "KRW", "PYG", "RWF", "UGX", "VND", "VUV", "XAF", "XOF", "XPF"]);
  const value = zeroDecimalCurrencies.has(normalizedCurrency) ? amount : amount / 100;
  try {
    return new Intl.NumberFormat("ja-JP", {
      style: "currency",
      currency: normalizedCurrency,
    }).format(value);
  } catch {
    return `${numberFormatter.format(amount)} ${normalizedCurrency}`;
  }
}

function invoiceStatusLabel(status: string) {
  const labels: Record<string, string> = {
    draft: "下書き",
    open: "支払い待ち",
    paid: "支払済み",
    uncollectible: "回収不能",
    void: "無効",
  };
  return labels[status] ?? status;
}

function safeExternalURL(value?: string) {
  if (!value) return null;
  try {
    const url = new URL(value);
    return url.protocol === "https:" ? url.toString() : null;
  } catch {
    return null;
  }
}

function errorMessage(caught: unknown, fallback: string) {
  return caught instanceof ApiClientError ? caught.message : fallback;
}

export function OrganizationConsole() {
  const router = useRouter();
  const detailRequestSequence = useRef(0);
  const [user, setUser] = useState<AuthUser | null>(null);
  const [organizations, setOrganizations] = useState<Organization[]>([]);
  const [selectedOrganizationId, setSelectedOrganizationId] = useState("");
  const [detail, setDetail] = useState<OrganizationDetailResponse | null>(null);
  const [currentRole, setCurrentRole] = useState<OrganizationRole | null>(null);
  const [billing, setBilling] = useState<OrganizationBillingDetails | null>(null);
  const [invoicePage, setInvoicePage] = useState<OrganizationInvoiceListResponse | null>(null);
  const [organizationName, setOrganizationName] = useState("");
  const [memberUserId, setMemberUserId] = useState("");
  const [memberRole, setMemberRole] = useState<OrganizationRole>("member");
  const [isInitialLoading, setIsInitialLoading] = useState(true);
  const [isDetailLoading, setIsDetailLoading] = useState(false);
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [isInvoiceLoading, setIsInvoiceLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [billingError, setBillingError] = useState<string | null>(null);
  const [invoiceError, setInvoiceError] = useState<string | null>(null);

  const redirectIfUnauthorized = useCallback(
    (caught: unknown) => {
      if (caught instanceof ApiClientError && caught.status === 401) {
        router.replace("/auth");
        return true;
      }
      return false;
    },
    [router],
  );

  const loadInitial = useCallback(async () => {
    setIsInitialLoading(true);
    setError(null);
    try {
      const [meResponse, organizationResponse] = await Promise.all([
        api.me(),
        api.listOrganizations(),
      ]);
      setUser(meResponse.user);
      setOrganizations(organizationResponse.items);
      setSelectedOrganizationId((current) => {
        if (organizationResponse.items.some((organization) => organization.id === current)) {
          return current;
        }
        return organizationResponse.items[0]?.id ?? "";
      });
    } catch (caught) {
      if (!redirectIfUnauthorized(caught)) {
        setError(errorMessage(caught, "組織情報の取得に失敗しました。"));
      }
    } finally {
      setIsInitialLoading(false);
    }
  }, [redirectIfUnauthorized]);

  const loadOrganization = useCallback(
    async (organizationId: string) => {
      const requestSequence = detailRequestSequence.current + 1;
      detailRequestSequence.current = requestSequence;
      setIsDetailLoading(true);
      setError(null);
      setBillingError(null);
      setInvoiceError(null);
      setDetail(null);
      setCurrentRole(null);
      setBilling(null);
      setInvoicePage(null);

      try {
        const organizationDetail = await api.getOrganization(organizationId);
        if (requestSequence !== detailRequestSequence.current) return;

        const role = organizationDetail.members.find(
          (member) => member.user_id === user?.id,
        )?.role ?? null;
        setDetail(organizationDetail);
        setCurrentRole(role);

        const billingPromise = role === "owner"
          ? api.getOrganizationBilling(organizationId)
          : Promise.resolve(null);
        const invoicePromise = role === "owner" || role === "admin"
          ? api.listOrganizationInvoices(organizationId, INVOICE_PAGE_SIZE)
          : Promise.resolve(null);
        const [billingResult, invoiceResult] = await Promise.allSettled([
          billingPromise,
          invoicePromise,
        ] as const);
        if (requestSequence !== detailRequestSequence.current) return;

        if (billingResult.status === "fulfilled") {
          setBilling(billingResult.value);
        } else if (!redirectIfUnauthorized(billingResult.reason)) {
          setBillingError(
            errorMessage(billingResult.reason, "課金情報を取得できませんでした。"),
          );
        }

        if (invoiceResult.status === "fulfilled") {
          setInvoicePage(invoiceResult.value);
        } else if (!redirectIfUnauthorized(invoiceResult.reason)) {
          setInvoiceError(
            errorMessage(invoiceResult.reason, "請求書を取得できませんでした。"),
          );
        }
      } catch (caught) {
        if (!redirectIfUnauthorized(caught)) {
          setError(errorMessage(caught, "組織詳細の取得に失敗しました。"));
        }
      } finally {
        if (requestSequence === detailRequestSequence.current) {
          setIsDetailLoading(false);
        }
      }
    },
    [redirectIfUnauthorized, user?.id],
  );

  useEffect(() => {
    void loadInitial();
  }, [loadInitial]);

  useEffect(() => {
    if (selectedOrganizationId && user) {
      void loadOrganization(selectedOrganizationId);
    } else {
      detailRequestSequence.current += 1;
      setDetail(null);
      setCurrentRole(null);
      setBilling(null);
      setInvoicePage(null);
      setIsDetailLoading(false);
    }
  }, [loadOrganization, selectedOrganizationId, user]);

  const canManageMembers = currentRole === "owner" || currentRole === "admin";
  const canViewInvoices = currentRole === "owner" || currentRole === "admin";

  const seatUsagePercent = useMemo(() => {
    if (!billing || billing.seat_limit <= 0) return 0;
    return Math.min(100, Math.round((billing.current_seat_usage / billing.seat_limit) * 100));
  }, [billing]);

  async function handleCreateOrganization() {
    const name = organizationName.trim();
    if (!name) {
      setError("組織名を入力してください。");
      return;
    }
    setIsSubmitting(true);
    setError(null);
    setNotice(null);
    try {
      const created = await api.createOrganization(name);
      setOrganizations((current) => [...current, created]);
      setSelectedOrganizationId(created.id);
      setOrganizationName("");
      setNotice("組織を作成しました。");
    } catch (caught) {
      if (!redirectIfUnauthorized(caught)) {
        setError(errorMessage(caught, "組織の作成に失敗しました。"));
      }
    } finally {
      setIsSubmitting(false);
    }
  }

  async function handleAddMember() {
    if (!selectedOrganizationId || !memberUserId.trim()) {
      setError("追加するユーザーIDを入力してください。");
      return;
    }
    setIsSubmitting(true);
    setError(null);
    setNotice(null);
    try {
      await api.addOrganizationMember(selectedOrganizationId, {
        user_id: memberUserId.trim(),
        role: memberRole,
      });
      setMemberUserId("");
      setMemberRole("member");
      setNotice("メンバーを追加しました。");
      await loadOrganization(selectedOrganizationId);
    } catch (caught) {
      if (!redirectIfUnauthorized(caught)) {
        setError(errorMessage(caught, "メンバーの追加に失敗しました。"));
      }
    } finally {
      setIsSubmitting(false);
    }
  }

  async function handleRemoveMember(userId: string) {
    if (!selectedOrganizationId) return;
    if (!window.confirm(`ユーザー ${userId} を組織から削除しますか？`)) return;

    setIsSubmitting(true);
    setError(null);
    setNotice(null);
    try {
      await api.removeOrganizationMember(selectedOrganizationId, userId);
      setNotice("メンバーを削除しました。");
      await loadOrganization(selectedOrganizationId);
    } catch (caught) {
      if (!redirectIfUnauthorized(caught)) {
        setError(errorMessage(caught, "メンバーの削除に失敗しました。"));
      }
    } finally {
      setIsSubmitting(false);
    }
  }

  async function handleCheckout() {
    if (!selectedOrganizationId) return;
    setIsSubmitting(true);
    setError(null);
    try {
      const checkout = await api.createOrganizationCheckout(selectedOrganizationId);
      const checkoutURL = new URL(checkout.url, window.location.origin);
      if (checkoutURL.protocol !== "https:" && checkoutURL.hostname !== "localhost") {
        throw new Error("不正なCheckout URLです");
      }
      window.location.assign(checkoutURL.toString());
    } catch (caught) {
      if (!redirectIfUnauthorized(caught)) {
        setError(errorMessage(caught, "Checkoutの開始に失敗しました。"));
      }
      setIsSubmitting(false);
    }
  }

  async function handleLoadMoreInvoices() {
    if (!selectedOrganizationId || !invoicePage?.next_starting_after) return;
    setIsInvoiceLoading(true);
    setInvoiceError(null);
    try {
      const nextPage = await api.listOrganizationInvoices(
        selectedOrganizationId,
        INVOICE_PAGE_SIZE,
        invoicePage.next_starting_after,
      );
      setInvoicePage((current) => ({
        invoices: [...(current?.invoices ?? []), ...nextPage.invoices],
        has_more: nextPage.has_more,
        next_starting_after: nextPage.next_starting_after,
      }));
    } catch (caught) {
      if (!redirectIfUnauthorized(caught)) {
        setInvoiceError(errorMessage(caught, "追加の請求書を取得できませんでした。"));
      }
    } finally {
      setIsInvoiceLoading(false);
    }
  }

  async function handleLogout() {
    setIsSubmitting(true);
    setError(null);
    try {
      await api.logout();
      router.replace("/auth");
      router.refresh();
    } catch (caught) {
      if (!redirectIfUnauthorized(caught)) {
        setError(errorMessage(caught, "ログアウトに失敗しました。"));
      }
      setIsSubmitting(false);
    }
  }

  return (
    <section className="shell page-section">
      <div className="page-heading">
        <div>
          <p className="eyebrow">Organization workspace</p>
          <h1>組織管理</h1>
          <p>メンバー、利用量、seat、請求書を一つの画面で管理します。</p>
        </div>
        <div className="inline-actions">
          {user && <span className="meta-text">{user.display_name || user.email}</span>}
          <button
            className="button button-secondary button-small"
            type="button"
            onClick={handleLogout}
            disabled={isSubmitting}
          >
            ログアウト
          </button>
        </div>
      </div>

      {error && <p className="alert alert-error" role="alert">{error}</p>}
      {notice && <p className="alert alert-success" role="status">{notice}</p>}

      {isInitialLoading ? (
        <div className="panel loading-state" aria-live="polite">組織情報を読み込んでいます…</div>
      ) : (
        <div className={styles.workspace}>
          <aside className={`panel ${styles.sidebar}`} aria-label="組織の選択と作成">
            <div className={styles.sectionHeading}>
              <div>
                <h2>所属組織</h2>
                <p>{organizations.length}件</p>
              </div>
            </div>

            {organizations.length > 0 ? (
              <div className={styles.organizationList}>
                {organizations.map((organization) => (
                  <button
                    key={organization.id}
                    className={styles.organizationButton}
                    data-active={organization.id === selectedOrganizationId}
                    type="button"
                    onClick={() => setSelectedOrganizationId(organization.id)}
                  >
                    <strong>{organization.name}</strong>
                    <span>{organization.id}</span>
                  </button>
                ))}
              </div>
            ) : (
              <p className={styles.emptyMessage}>所属組織はありません。最初の組織を作成してください。</p>
            )}

            <div className={styles.createArea}>
              <div className="field">
                <label htmlFor="organization-name">新しい組織名</label>
                <input
                  id="organization-name"
                  value={organizationName}
                  onChange={(event) => setOrganizationName(event.target.value)}
                  placeholder="例: VaultSend開発部"
                  maxLength={120}
                />
              </div>
              <button
                className="button"
                type="button"
                onClick={handleCreateOrganization}
                disabled={isSubmitting || !organizationName.trim()}
              >
                組織を作成
              </button>
            </div>
          </aside>

          <div className={styles.content}>
            {!selectedOrganizationId ? (
              <div className="panel empty-state">組織を作成すると、メンバーと課金情報を管理できます。</div>
            ) : isDetailLoading ? (
              <div className="panel loading-state" aria-live="polite">選択した組織を読み込んでいます…</div>
            ) : detail ? (
              <>
                <section className={`panel ${styles.summary}`}>
                  <div className={styles.sectionHeading}>
                    <div>
                      <h2>{detail.organization.name}</h2>
                      <p>あなたの権限: {currentRole ? roleLabels[currentRole] : "確認中"}</p>
                    </div>
                    <span className="status-badge" data-status="sent">
                      {detail.members.length} メンバー
                    </span>
                  </div>
                  <dl className={styles.definitionList}>
                    <div>
                      <dt>組織ID</dt>
                      <dd>{detail.organization.id}</dd>
                    </div>
                    <div>
                      <dt>作成者ユーザーID</dt>
                      <dd>{detail.organization.owner_user_id}</dd>
                    </div>
                  </dl>
                </section>

                <section className={`panel ${styles.section}`}>
                  <div className={styles.sectionHeading}>
                    <div>
                      <h2>メンバー</h2>
                      <p>権限とseat利用者を確認します。</p>
                    </div>
                  </div>

                  <div className={styles.tableWrapper}>
                    <table className={styles.table}>
                      <thead>
                        <tr>
                          <th>ユーザーID</th>
                          <th>権限</th>
                          <th><span className={styles.visuallyHidden}>操作</span></th>
                        </tr>
                      </thead>
                      <tbody>
                        {detail.members.map((member) => (
                          <tr key={member.user_id}>
                            <td>
                              <code>{member.user_id}</code>
                              {member.user_id === user?.id && <span className={styles.selfLabel}>自分</span>}
                            </td>
                            <td><span className="status-badge">{roleLabels[member.role]}</span></td>
                            <td className={styles.actionCell}>
                              {canManageMembers &&
                              member.user_id !== user?.id &&
                              member.role !== "owner" ? (
                                <button
                                  className="button button-secondary button-small"
                                  type="button"
                                  onClick={() => handleRemoveMember(member.user_id)}
                                  disabled={isSubmitting}
                                >
                                  削除
                                </button>
                              ) : null}
                            </td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>

                  {canManageMembers ? (
                    <div className={styles.memberForm}>
                      <div className="field">
                        <label htmlFor="member-user-id">追加するユーザーID</label>
                        <input
                          id="member-user-id"
                          value={memberUserId}
                          onChange={(event) => setMemberUserId(event.target.value)}
                          placeholder="UUIDを入力"
                          autoComplete="off"
                        />
                      </div>
                      <div className={styles.selectField}>
                        <label htmlFor="member-role">権限</label>
                        <select
                          id="member-role"
                          value={memberRole}
                          onChange={(event) => setMemberRole(event.target.value as OrganizationRole)}
                        >
                          <option value="member">メンバー</option>
                          <option value="admin">管理者</option>
                          {currentRole === "owner" && <option value="owner">オーナー</option>}
                        </select>
                      </div>
                      <button
                        className="button"
                        type="button"
                        onClick={handleAddMember}
                        disabled={isSubmitting || !memberUserId.trim()}
                      >
                        メンバーを追加
                      </button>
                    </div>
                  ) : (
                    <p className={styles.permissionNote}>メンバーの追加・削除には管理者以上の権限が必要です。</p>
                  )}
                </section>

                {currentRole === "owner" && (
                  <section className={`panel ${styles.section}`}>
                    <div className={styles.sectionHeading}>
                      <div>
                        <h2>プランと利用量</h2>
                        <p>organization単位の課金とseatを確認します。</p>
                      </div>
                      {billing && (
                        <button
                          className="button button-small"
                          type="button"
                          onClick={handleCheckout}
                          disabled={isSubmitting}
                        >
                          {billing.plan === "pro" ? "プランを管理" : "Proを申し込む"}
                        </button>
                      )}
                    </div>

                    {billingError && <p className="alert alert-error" role="alert">{billingError}</p>}
                    {billing ? (
                      <>
                        <div className={styles.metrics} aria-label="課金・利用量サマリー">
                          <div className="metric">
                            <span className="metric-label">現在のプラン</span>
                            <strong className="metric-value">{billing.plan.toUpperCase()}</strong>
                            <span className="meta-text">{billing.status}</span>
                          </div>
                          <div className="metric">
                            <span className="metric-label">今月の送信</span>
                            <strong className="metric-value">{numberFormatter.format(billing.usage.current_month_shipments)}</strong>
                            <span className="meta-text">
                              {billing.remaining.remaining_shipments === undefined
                                ? "上限なし"
                                : `残り ${numberFormatter.format(billing.remaining.remaining_shipments)}件`}
                            </span>
                          </div>
                          <div className="metric">
                            <span className="metric-label">使用ストレージ</span>
                            <strong className="metric-value">{formatBytes(billing.usage.current_storage_bytes)}</strong>
                            <span className="meta-text">現在保存中のファイル</span>
                          </div>
                          <div className="metric">
                            <span className="metric-label">seat</span>
                            <strong className="metric-value">{billing.current_seat_usage} / {billing.seat_limit}</strong>
                            <span className="meta-text">残り {billing.remaining_seats} seat</span>
                          </div>
                        </div>
                        <div className={styles.seatProgress}>
                          <div className={styles.seatProgressLabel}>
                            <span>seat使用率</span>
                            <strong>{seatUsagePercent}%</strong>
                          </div>
                          <div className="progress-track" aria-hidden="true">
                            <div className="progress-value" style={{ width: `${seatUsagePercent}%` }} />
                          </div>
                          <p>次回請求日: {formatDate(billing.next_billing_at)}</p>
                        </div>
                      </>
                    ) : !billingError ? (
                      <p className="loading-state" aria-live="polite">課金情報を読み込んでいます…</p>
                    ) : null}
                  </section>
                )}

                {canViewInvoices && (
                  <section className={`panel ${styles.section}`}>
                    <div className={styles.sectionHeading}>
                      <div>
                        <h2>請求書</h2>
                        <p>Stripeで発行されたorganizationの請求履歴です。</p>
                      </div>
                    </div>

                    {invoiceError && <p className="alert alert-error" role="alert">{invoiceError}</p>}
                    {invoicePage ? (
                      invoicePage.invoices.length > 0 ? (
                        <>
                          <div className={styles.invoiceList}>
                            {invoicePage.invoices.map((invoice: InvoiceSummary) => {
                              const hostedInvoiceURL = safeExternalURL(invoice.hosted_invoice_url);
                              const invoicePDFURL = safeExternalURL(invoice.invoice_pdf);
                              return (
                                <article className={styles.invoiceRow} key={invoice.invoice_id}>
                                  <div>
                                    <strong>{formatMoney(invoice.amount, invoice.currency)}</strong>
                                    <span>{formatDate(invoice.created_at)} · {invoice.invoice_id}</span>
                                  </div>
                                  <span className="status-badge" data-status={invoice.status === "paid" ? "sent" : undefined}>
                                    {invoiceStatusLabel(invoice.status)}
                                  </span>
                                  <div className={styles.invoiceActions}>
                                    {hostedInvoiceURL && (
                                      <a className="button button-secondary button-small" href={hostedInvoiceURL} target="_blank" rel="noreferrer">
                                        詳細
                                      </a>
                                    )}
                                    {invoicePDFURL && (
                                      <a className="button button-secondary button-small" href={invoicePDFURL} target="_blank" rel="noreferrer">
                                        PDF
                                      </a>
                                    )}
                                  </div>
                                </article>
                              );
                            })}
                          </div>
                          {invoicePage.has_more && invoicePage.next_starting_after && (
                            <button
                              className="button button-secondary"
                              type="button"
                              onClick={handleLoadMoreInvoices}
                              disabled={isInvoiceLoading}
                            >
                              {isInvoiceLoading ? "読み込み中…" : "過去の請求書をさらに表示"}
                            </button>
                          )}
                        </>
                      ) : (
                        <p className={styles.emptyMessage}>請求書はまだありません。</p>
                      )
                    ) : !invoiceError ? (
                      <p className="loading-state" aria-live="polite">請求書を読み込んでいます…</p>
                    ) : null}
                  </section>
                )}

                {currentRole === "member" && (
                  <section className={`panel ${styles.permissionPanel}`}>
                    <h2>権限について</h2>
                    <p>課金・請求書の閲覧には管理者以上、プラン変更にはオーナー権限が必要です。</p>
                  </section>
                )}
              </>
            ) : (
              <div className="panel empty-state">組織情報を表示できませんでした。</div>
            )}
          </div>
        </div>
      )}
    </section>
  );
}
