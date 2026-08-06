"use client";

import { useCallback, useEffect, useState } from "react";
import Link from "next/link";
import { useParams, useRouter } from "next/navigation";
import { api, ApiClientError } from "@/lib/api";
import type {
  AuthUser,
  OrganizationInvitationAcceptResponse,
  OrganizationInvitationInspectResponse,
} from "@/lib/types";
import styles from "./invitation-acceptance-panel.module.css";

const dateFormatter = new Intl.DateTimeFormat("ja-JP", {
  year: "numeric",
  month: "long",
  day: "numeric",
  hour: "2-digit",
  minute: "2-digit",
});

function formatDate(value: string) {
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? "—" : dateFormatter.format(date);
}

function roleLabel(role: string) {
  return role === "admin" ? "管理者" : "メンバー";
}

export function InvitationAcceptancePanel() {
  const params = useParams<{ token: string }>();
  const router = useRouter();
  const token = typeof params.token === "string" ? params.token : "";
  const [invitation, setInvitation] = useState<OrganizationInvitationInspectResponse | null>(null);
  const [user, setUser] = useState<AuthUser | null>(null);
  const [accepted, setAccepted] = useState<OrganizationInvitationAcceptResponse | null>(null);
  const [isLoading, setIsLoading] = useState(true);
  const [isAccepting, setIsAccepting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const loadInvitation = useCallback(async () => {
    if (!token) {
      setError("招待URLが不正です。メールに記載されたリンクを開き直してください。");
      setIsLoading(false);
      return;
    }

    setIsLoading(true);
    setError(null);
    const [invitationResult, userResult] = await Promise.allSettled([
      api.inspectOrganizationInvitation(token),
      api.me(),
    ]);

    if (invitationResult.status === "fulfilled") {
      setInvitation(invitationResult.value);
    } else {
      setError(
        invitationResult.reason instanceof ApiClientError
          ? invitationResult.reason.message
          : "招待情報を確認できませんでした。",
      );
    }

    if (userResult.status === "fulfilled") {
      setUser(userResult.value.user);
    } else if (
      !(userResult.reason instanceof ApiClientError && userResult.reason.status === 401)
    ) {
      setError((current) => current ?? "ログイン状態を確認できませんでした。");
    }
    setIsLoading(false);
  }, [token]);

  useEffect(() => {
    void loadInvitation();
  }, [loadInvitation]);

  async function handleAccept() {
    if (!token || !user || isAccepting) return;
    setIsAccepting(true);
    setError(null);
    try {
      const response = await api.acceptOrganizationInvitation(token);
      setAccepted(response);
      setInvitation((current) => (current ? { ...current, status: "accepted" } : current));
    } catch (caught) {
      if (caught instanceof ApiClientError && caught.status === 401) {
        router.push(loginPath(token));
        return;
      }
      setError(
        caught instanceof ApiClientError
          ? caught.message
          : "招待を承認できませんでした。",
      );
    } finally {
      setIsAccepting(false);
    }
  }

  if (isLoading) {
    return (
      <section className="shell page-section">
        <div className="panel loading-state" aria-live="polite">招待情報を確認しています…</div>
      </section>
    );
  }

  return (
    <section className={`shell page-section ${styles.wrap}`}>
      <div className={`panel ${styles.card}`}>
        {accepted ? (
          <div className={styles.success}>
            <div className={styles.icon} aria-hidden="true">✓</div>
            <div>
              <p className="eyebrow">Invitation accepted</p>
              <h1>{accepted.organization.name} に参加しました</h1>
              <p>
                {roleLabel(accepted.member.role)}として組織へ追加されました。
                組織の送信履歴や利用可能な機能を確認できます。
              </p>
            </div>
            <div className="inline-actions">
              <Link className="button" href="/organizations">組織管理を開く</Link>
              <Link className="button button-secondary" href="/shipments">送信履歴を開く</Link>
            </div>
          </div>
        ) : invitation ? (
          <>
            <div className={styles.header}>
              <p className="eyebrow">Organization invitation</p>
              <h1>{invitation.organization_name} への招待</h1>
              <p>招待内容を確認し、対象のメールアドレスでログインして参加してください。</p>
            </div>

            <dl className={styles.details}>
              <div>
                <dt>組織</dt>
                <dd>{invitation.organization_name}</dd>
              </div>
              <div>
                <dt>招待先</dt>
                <dd>{invitation.email_masked}</dd>
              </div>
              <div>
                <dt>権限</dt>
                <dd>{roleLabel(invitation.role)}</dd>
              </div>
              <div>
                <dt>有効期限</dt>
                <dd>{formatDate(invitation.expires_at)}</dd>
              </div>
            </dl>

            {error && <p className="alert alert-error" role="alert">{error}</p>}

            {invitation.status === "pending" ? (
              user ? (
                <div className={styles.actionArea}>
                  <p>
                    <strong>{user.email}</strong> でログインしています。
                    招待先と一致する場合のみ参加できます。
                  </p>
                  <button
                    className="button"
                    type="button"
                    onClick={handleAccept}
                    disabled={isAccepting}
                  >
                    {isAccepting ? "参加処理中…" : "この組織に参加する"}
                  </button>
                </div>
              ) : (
                <div className={styles.actionArea}>
                  <p>招待を承認するには、招待先と同じメールアドレスでログインまたは新規登録してください。</p>
                  <Link className="button" href={loginPath(token)}>ログイン・新規登録へ</Link>
                </div>
              )
            ) : (
              <div className={styles.unavailable}>
                {invitation.status === "expired" && "この招待は有効期限が切れています。招待者へ再送を依頼してください。"}
                {invitation.status === "revoked" && "この招待は取り消されています。"}
                {invitation.status === "accepted" && "この招待は既に承認されています。"}
              </div>
            )}
          </>
        ) : (
          <div className={styles.header}>
            <p className="eyebrow">Invitation unavailable</p>
            <h1>招待を確認できません</h1>
            <p>{error ?? "招待URLが無効か、有効期限が切れている可能性があります。"}</p>
            <Link className="button button-secondary" href="/">トップへ戻る</Link>
          </div>
        )}
      </div>
    </section>
  );
}

function loginPath(token: string) {
  return `/auth?next=${encodeURIComponent(`/invite/${token}`)}`;
}
