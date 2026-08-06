"use client";

import { FormEvent, useCallback, useEffect, useMemo, useState } from "react";
import { useRouter } from "next/navigation";
import { api, ApiClientError } from "@/lib/api";
import type {
  AuthUser,
  InvitationRole,
  InvitationStatus,
  Organization,
  OrganizationInvitation,
  OrganizationRole,
} from "@/lib/types";
import styles from "./invitation-management-panel.module.css";

const dateFormatter = new Intl.DateTimeFormat("ja-JP", {
  year: "numeric",
  month: "short",
  day: "numeric",
  hour: "2-digit",
  minute: "2-digit",
});

const roleLabels: Record<InvitationRole, string> = {
  admin: "管理者",
  member: "メンバー",
};

const statusLabels: Record<InvitationStatus, string> = {
  pending: "招待中",
  accepted: "承認済み",
  revoked: "取消済み",
  expired: "期限切れ",
};

function formatDate(value?: string) {
  if (!value) return "—";
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? "—" : dateFormatter.format(date);
}

function toErrorMessage(caught: unknown, fallback: string) {
  return caught instanceof ApiClientError ? caught.message : fallback;
}

export function InvitationManagementPanel() {
  const router = useRouter();
  const [user, setUser] = useState<AuthUser | null>(null);
  const [organizations, setOrganizations] = useState<Organization[]>([]);
  const [selectedOrganizationId, setSelectedOrganizationId] = useState("");
  const [currentRole, setCurrentRole] = useState<OrganizationRole | null>(null);
  const [invitations, setInvitations] = useState<OrganizationInvitation[]>([]);
  const [email, setEmail] = useState("");
  const [role, setRole] = useState<InvitationRole>("member");
  const [isInitialLoading, setIsInitialLoading] = useState(true);
  const [isOrganizationLoading, setIsOrganizationLoading] = useState(false);
  const [activeInvitationId, setActiveInvitationId] = useState<string | null>(null);
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);

  const redirectIfUnauthorized = useCallback(
    (caught: unknown) => {
      if (caught instanceof ApiClientError && caught.status === 401) {
        router.replace(`/auth?next=${encodeURIComponent("/invitations")}`);
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
        setError(toErrorMessage(caught, "組織情報を取得できませんでした。"));
      }
    } finally {
      setIsInitialLoading(false);
    }
  }, [redirectIfUnauthorized]);

  const loadOrganization = useCallback(
    async (organizationId: string) => {
      if (!user) return;
      setIsOrganizationLoading(true);
      setError(null);
      setInvitations([]);
      setCurrentRole(null);
      try {
        const detail = await api.getOrganization(organizationId);
        const resolvedRole =
          detail.members.find((member) => member.user_id === user.id)?.role ?? null;
        setCurrentRole(resolvedRole);
        if (resolvedRole === "owner" || resolvedRole === "admin") {
          const response = await api.listOrganizationInvitations(organizationId);
          setInvitations(response.items);
        }
      } catch (caught) {
        if (!redirectIfUnauthorized(caught)) {
          setError(toErrorMessage(caught, "招待情報を取得できませんでした。"));
        }
      } finally {
        setIsOrganizationLoading(false);
      }
    },
    [redirectIfUnauthorized, user],
  );

  useEffect(() => {
    void loadInitial();
  }, [loadInitial]);

  useEffect(() => {
    if (selectedOrganizationId && user) {
      void loadOrganization(selectedOrganizationId);
    } else {
      setCurrentRole(null);
      setInvitations([]);
    }
  }, [loadOrganization, selectedOrganizationId, user]);

  const selectedOrganization = useMemo(
    () => organizations.find((organization) => organization.id === selectedOrganizationId) ?? null,
    [organizations, selectedOrganizationId],
  );

  const pendingCount = useMemo(
    () => invitations.filter((invitation) => invitation.status === "pending").length,
    [invitations],
  );

  const canManage = currentRole === "owner" || currentRole === "admin";

  async function handleCreate(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const normalizedEmail = email.trim();
    if (!selectedOrganizationId || !normalizedEmail || isSubmitting) return;

    setIsSubmitting(true);
    setError(null);
    setNotice(null);
    try {
      const created = await api.createOrganizationInvitation(selectedOrganizationId, {
        email: normalizedEmail,
        role,
      });
      setInvitations((current) => [created, ...current]);
      setEmail("");
      setRole("member");
      setNotice(`${created.email} へ招待メールを送信キューに登録しました。`);
    } catch (caught) {
      if (!redirectIfUnauthorized(caught)) {
        setError(toErrorMessage(caught, "招待を作成できませんでした。"));
      }
    } finally {
      setIsSubmitting(false);
    }
  }

  async function handleResend(invitationId: string) {
    if (!selectedOrganizationId || activeInvitationId) return;
    setActiveInvitationId(invitationId);
    setError(null);
    setNotice(null);
    try {
      const refreshed = await api.resendOrganizationInvitation(
        selectedOrganizationId,
        invitationId,
      );
      setInvitations((current) =>
        current.map((invitation) =>
          invitation.id === invitationId ? refreshed : invitation,
        ),
      );
      setNotice("招待トークンを更新し、メールを再送しました。");
    } catch (caught) {
      if (!redirectIfUnauthorized(caught)) {
        setError(toErrorMessage(caught, "招待を再送できませんでした。"));
      }
    } finally {
      setActiveInvitationId(null);
    }
  }

  async function handleRevoke(invitation: OrganizationInvitation) {
    if (!selectedOrganizationId || activeInvitationId) return;
    const accepted = window.confirm(`${invitation.email} への招待を取り消しますか？`);
    if (!accepted) return;

    setActiveInvitationId(invitation.id);
    setError(null);
    setNotice(null);
    try {
      await api.revokeOrganizationInvitation(selectedOrganizationId, invitation.id);
      setInvitations((current) =>
        current.map((item) =>
          item.id === invitation.id ? { ...item, status: "revoked" } : item,
        ),
      );
      setNotice("招待を取り消しました。既存の招待リンクは利用できません。");
    } catch (caught) {
      if (!redirectIfUnauthorized(caught)) {
        setError(toErrorMessage(caught, "招待を取り消せませんでした。"));
      }
    } finally {
      setActiveInvitationId(null);
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
        setError(toErrorMessage(caught, "ログアウトできませんでした。"));
      }
      setIsSubmitting(false);
    }
  }

  return (
    <section className="shell page-section">
      <div className="page-heading">
        <div>
          <p className="eyebrow">Invitation workspace</p>
          <h1>組織招待</h1>
          <p>メールアドレスを指定し、期限付きリンクで安全にメンバーを招待します。</p>
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
        <div className="panel loading-state" aria-live="polite">招待情報を読み込んでいます…</div>
      ) : organizations.length === 0 ? (
        <div className="panel empty-state">
          <p>所属組織がありません。先に組織を作成してください。</p>
          <button className="button" type="button" onClick={() => router.push("/organizations")}>
            組織管理を開く
          </button>
        </div>
      ) : (
        <div className={styles.workspace}>
          <aside className={`panel ${styles.sidebar}`} aria-label="招待対象の組織">
            <div className={styles.sectionHeading}>
              <div>
                <h2>対象組織</h2>
                <p>{organizations.length}件</p>
              </div>
            </div>
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
          </aside>

          <div className={styles.content}>
            <section className={`panel ${styles.section}`} aria-labelledby="invitation-summary-title">
              <div className={styles.sectionHeading}>
                <div>
                  <h2 id="invitation-summary-title">{selectedOrganization?.name ?? "組織"}</h2>
                  <p>
                    {canManage
                      ? `有効な招待 ${pendingCount}件`
                      : "招待を管理するには管理者以上の権限が必要です。"}
                  </p>
                </div>
                {currentRole && <span className="status-badge">{currentRole}</span>}
              </div>

              {isOrganizationLoading ? (
                <div className="loading-state" aria-live="polite">組織の招待を読み込んでいます…</div>
              ) : canManage ? (
                <form className={styles.invitationForm} onSubmit={handleCreate}>
                  <div className="field">
                    <label htmlFor="invitation-email">招待するメールアドレス</label>
                    <input
                      id="invitation-email"
                      name="email"
                      type="email"
                      maxLength={320}
                      autoComplete="email"
                      required
                      value={email}
                      onChange={(event) => setEmail(event.target.value)}
                      placeholder="member@example.com"
                    />
                  </div>
                  <div className={styles.selectField}>
                    <label htmlFor="invitation-role">権限</label>
                    <select
                      id="invitation-role"
                      value={role}
                      onChange={(event) => setRole(event.target.value as InvitationRole)}
                    >
                      <option value="member">メンバー</option>
                      <option value="admin">管理者</option>
                    </select>
                  </div>
                  <button
                    className="button"
                    type="submit"
                    disabled={isSubmitting || !email.trim()}
                  >
                    {isSubmitting ? "送信中…" : "招待メールを送る"}
                  </button>
                </form>
              ) : (
                <div className={styles.permissionMessage}>
                  組織のownerまたはadminへ招待操作を依頼してください。
                </div>
              )}
            </section>

            {canManage && !isOrganizationLoading && (
              <section className={`panel ${styles.section}`} aria-labelledby="invitation-list-title">
                <div className={styles.sectionHeading}>
                  <div>
                    <h2 id="invitation-list-title">招待履歴</h2>
                    <p>再送すると以前のリンクは無効になり、有効期限が7日後へ更新されます。</p>
                  </div>
                </div>

                {invitations.length === 0 ? (
                  <div className="empty-state">この組織の招待履歴はありません。</div>
                ) : (
                  <div className={styles.tableWrapper}>
                    <table className={styles.table}>
                      <thead>
                        <tr>
                          <th>メールアドレス</th>
                          <th>権限</th>
                          <th>状態</th>
                          <th>有効期限</th>
                          <th>最終送信</th>
                          <th className={styles.actionCell}>操作</th>
                        </tr>
                      </thead>
                      <tbody>
                        {invitations.map((invitation) => {
                          const canOperate =
                            invitation.status === "pending" || invitation.status === "expired";
                          const busy = activeInvitationId === invitation.id;
                          return (
                            <tr key={invitation.id}>
                              <td>{invitation.email}</td>
                              <td>{roleLabels[invitation.role]}</td>
                              <td>
                                <span className="status-badge" data-status={invitation.status}>
                                  {statusLabels[invitation.status]}
                                </span>
                              </td>
                              <td>{formatDate(invitation.expires_at)}</td>
                              <td>{formatDate(invitation.last_sent_at)}</td>
                              <td className={styles.actionCell}>
                                {canOperate ? (
                                  <div className={styles.actions}>
                                    <button
                                      className="button button-secondary button-small"
                                      type="button"
                                      onClick={() => handleResend(invitation.id)}
                                      disabled={busy || activeInvitationId !== null}
                                    >
                                      {busy ? "処理中…" : "再送"}
                                    </button>
                                    {invitation.status === "pending" && (
                                      <button
                                        className="button button-danger button-small"
                                        type="button"
                                        onClick={() => handleRevoke(invitation)}
                                        disabled={busy || activeInvitationId !== null}
                                      >
                                        取消
                                      </button>
                                    )}
                                  </div>
                                ) : (
                                  <span className="meta-text">操作なし</span>
                                )}
                              </td>
                            </tr>
                          );
                        })}
                      </tbody>
                    </table>
                  </div>
                )}
              </section>
            )}
          </div>
        </div>
      )}
    </section>
  );
}
