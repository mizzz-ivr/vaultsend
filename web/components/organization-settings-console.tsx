"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useCallback, useEffect, useMemo, useState } from "react";
import { api, ApiClientError } from "@/lib/api";
import type {
  AuthUser,
  Organization,
  OrganizationDetailResponse,
  OrganizationRole,
} from "@/lib/types";
import styles from "./organization-settings-console.module.css";

const roleLabels: Record<OrganizationRole, string> = {
  owner: "オーナー",
  admin: "管理者",
  member: "メンバー",
};

function errorMessage(caught: unknown, fallback: string) {
  return caught instanceof ApiClientError ? caught.message : fallback;
}

export function OrganizationSettingsConsole() {
  const router = useRouter();
  const [user, setUser] = useState<AuthUser | null>(null);
  const [organizations, setOrganizations] = useState<Organization[]>([]);
  const [selectedOrganizationId, setSelectedOrganizationId] = useState("");
  const [detail, setDetail] = useState<OrganizationDetailResponse | null>(null);
  const [organizationName, setOrganizationName] = useState("");
  const [transferTargetUserId, setTransferTargetUserId] = useState("");
  const [isInitialLoading, setIsInitialLoading] = useState(true);
  const [isDetailLoading, setIsDetailLoading] = useState(false);
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [warning, setWarning] = useState<string | null>(null);

  const redirectIfUnauthorized = useCallback(
    (caught: unknown) => {
      if (caught instanceof ApiClientError && caught.status === 401) {
        router.replace("/auth?next=%2Forganization-settings");
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
        setError(errorMessage(caught, "組織設定の取得に失敗しました。"));
      }
    } finally {
      setIsInitialLoading(false);
    }
  }, [redirectIfUnauthorized]);

  const loadDetail = useCallback(
    async (organizationId: string) => {
      setIsDetailLoading(true);
      setError(null);
      setWarning(null);
      try {
        const organizationDetail = await api.getOrganization(organizationId);
        setDetail(organizationDetail);
        setOrganizationName(organizationDetail.organization.name);
        const role = organizationDetail.members.find((member) => member.user_id === user?.id)?.role;
        const firstTransferCandidate = role === "owner"
          ? organizationDetail.members.find(
              (member) => member.user_id !== user?.id && member.role !== "owner",
            )
          : undefined;
        setTransferTargetUserId(firstTransferCandidate?.user_id ?? "");
      } catch (caught) {
        if (!redirectIfUnauthorized(caught)) {
          setDetail(null);
          setError(errorMessage(caught, "組織詳細の取得に失敗しました。"));
        }
      } finally {
        setIsDetailLoading(false);
      }
    },
    [redirectIfUnauthorized, user?.id],
  );

  useEffect(() => {
    void loadInitial();
  }, [loadInitial]);

  useEffect(() => {
    if (selectedOrganizationId && user) {
      void loadDetail(selectedOrganizationId);
    } else {
      setDetail(null);
      setOrganizationName("");
      setTransferTargetUserId("");
    }
  }, [loadDetail, selectedOrganizationId, user]);

  const currentRole = useMemo(
    () => detail?.members.find((member) => member.user_id === user?.id)?.role ?? null,
    [detail, user?.id],
  );
  const canRename = currentRole === "owner" || currentRole === "admin";
  const canTransfer = currentRole === "owner";
  const canLeave = currentRole === "admin" || currentRole === "member";
  const transferCandidates = useMemo(
    () => detail?.members.filter(
      (member) => member.user_id !== user?.id && member.role !== "owner",
    ) ?? [],
    [detail, user?.id],
  );

  function updateOrganizationLocally(updated: Organization) {
    setOrganizations((current) => current.map((organization) => (
      organization.id === updated.id ? updated : organization
    )));
    setDetail((current) => current ? { ...current, organization: updated } : current);
  }

  async function handleRename() {
    if (!detail || !selectedOrganizationId || !canRename) return;
    const name = organizationName.trim();
    if (!name) {
      setError("組織名を入力してください。");
      return;
    }
    if (name === detail.organization.name) {
      setNotice("組織名に変更はありません。");
      return;
    }

    setIsSubmitting(true);
    setError(null);
    setNotice(null);
    setWarning(null);
    try {
      const updated = await api.updateOrganization(selectedOrganizationId, name);
      updateOrganizationLocally(updated);
      setOrganizationName(updated.name);
      setNotice("組織名を変更しました。");
    } catch (caught) {
      if (!redirectIfUnauthorized(caught)) {
        setError(errorMessage(caught, "組織名の変更に失敗しました。"));
      }
    } finally {
      setIsSubmitting(false);
    }
  }

  async function handleTransferOwnership() {
    if (!detail || !selectedOrganizationId || !canTransfer || !transferTargetUserId) return;
    const target = detail.members.find((member) => member.user_id === transferTargetUserId);
    if (!target) {
      setError("移譲先メンバーを選択してください。");
      return;
    }
    const confirmed = window.confirm(
      `ユーザー ${target.user_id} へオーナー権限を移譲しますか？\n移譲後、あなたの権限は管理者になります。`,
    );
    if (!confirmed) return;

    setIsSubmitting(true);
    setError(null);
    setNotice(null);
    setWarning(null);
    try {
      const transferred = await api.transferOrganizationOwner(
        selectedOrganizationId,
        transferTargetUserId,
      );
      setOrganizations((current) => current.map((organization) => (
        organization.id === transferred.organization.id
          ? transferred.organization
          : organization
      )));
      setDetail((current) => {
        if (!current) return current;
        return {
          organization: transferred.organization,
          members: current.members.map((member) => {
            if (member.user_id === transferred.previous_owner.user_id) {
              return transferred.previous_owner;
            }
            if (member.user_id === transferred.new_owner.user_id) {
              return transferred.new_owner;
            }
            return member;
          }),
        };
      });
      setTransferTargetUserId("");
      setNotice("オーナー権限を移譲しました。あなたは管理者として引き続き組織を管理できます。");
    } catch (caught) {
      if (!redirectIfUnauthorized(caught)) {
        setError(errorMessage(caught, "オーナー権限の移譲に失敗しました。"));
      }
    } finally {
      setIsSubmitting(false);
    }
  }

  function completeLeave(message: string, isWarning = false) {
    const remaining = organizations.filter((organization) => organization.id !== selectedOrganizationId);
    setOrganizations(remaining);
    setSelectedOrganizationId(remaining[0]?.id ?? "");
    setDetail(null);
    setOrganizationName("");
    setTransferTargetUserId("");
    if (isWarning) {
      setWarning(message);
      setNotice(null);
    } else {
      setNotice(message);
      setWarning(null);
    }
  }

  async function handleLeave() {
    if (!detail || !selectedOrganizationId || !canLeave) return;
    const confirmed = window.confirm(
      `${detail.organization.name} から退出しますか？\n退出後、この組織の送信履歴や設定にはアクセスできなくなります。`,
    );
    if (!confirmed) return;

    setIsSubmitting(true);
    setError(null);
    setNotice(null);
    setWarning(null);
    try {
      await api.leaveOrganization(selectedOrganizationId);
      completeLeave("組織から退出しました。");
    } catch (caught) {
      if (caught instanceof ApiClientError && caught.code === "seat_sync_failed_after_leave") {
        completeLeave(caught.message, true);
      } else if (!redirectIfUnauthorized(caught)) {
        setError(errorMessage(caught, "組織からの退出に失敗しました。"));
      }
    } finally {
      setIsSubmitting(false);
    }
  }

  return (
    <section className="shell page-section">
      <div className="page-heading">
        <div>
          <p className="eyebrow">Organization settings</p>
          <h1>組織設定</h1>
          <p>組織名、所有権、退出を安全に管理します。</p>
        </div>
        <div className="inline-actions">
          <Link className="button button-secondary button-small" href="/organizations">
            メンバー・課金管理
          </Link>
          <Link className="button button-secondary button-small" href="/invitations">
            招待管理
          </Link>
        </div>
      </div>

      {error && <p className="alert alert-error" role="alert">{error}</p>}
      {notice && <p className="alert alert-success" role="status">{notice}</p>}
      {warning && <p className="alert alert-error" role="status">{warning}</p>}

      {isInitialLoading ? (
        <div className="panel loading-state" aria-live="polite">組織設定を読み込んでいます…</div>
      ) : (
        <div className={styles.workspace}>
          <aside className={`panel ${styles.sidebar}`} aria-label="設定する組織を選択">
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
              <div className={styles.emptyMessage}>
                <p>設定できる組織はありません。</p>
                <Link href="/organizations">組織を作成する</Link>
              </div>
            )}
          </aside>

          <div className={styles.content}>
            {!selectedOrganizationId ? (
              <div className="panel empty-state">設定する組織を選択してください。</div>
            ) : isDetailLoading ? (
              <div className="panel loading-state" aria-live="polite">組織詳細を読み込んでいます…</div>
            ) : detail ? (
              <>
                <section className={`panel ${styles.summary}`}>
                  <div className={styles.sectionHeading}>
                    <div>
                      <h2>{detail.organization.name}</h2>
                      <p>あなたの権限: {currentRole ? roleLabels[currentRole] : "確認中"}</p>
                    </div>
                    <span className="status-badge">{detail.members.length} メンバー</span>
                  </div>
                  <dl className={styles.definitionList}>
                    <div>
                      <dt>組織ID</dt>
                      <dd><code>{detail.organization.id}</code></dd>
                    </div>
                    <div>
                      <dt>現在のオーナー</dt>
                      <dd><code>{detail.organization.owner_user_id}</code></dd>
                    </div>
                  </dl>
                </section>

                <section className={`panel ${styles.section}`}>
                  <div className={styles.sectionHeading}>
                    <div>
                      <h2>組織名</h2>
                      <p>管理者以上が組織名を変更できます。</p>
                    </div>
                  </div>
                  {canRename ? (
                    <div className={styles.inlineForm}>
                      <div className="field">
                        <label htmlFor="organization-settings-name">組織名</label>
                        <input
                          id="organization-settings-name"
                          value={organizationName}
                          onChange={(event) => setOrganizationName(event.target.value)}
                          maxLength={120}
                        />
                      </div>
                      <button
                        className="button"
                        type="button"
                        onClick={handleRename}
                        disabled={isSubmitting || !organizationName.trim()}
                      >
                        組織名を保存
                      </button>
                    </div>
                  ) : (
                    <p className={styles.permissionNote}>組織名の変更には管理者以上の権限が必要です。</p>
                  )}
                </section>

                <section className={`panel ${styles.section}`}>
                  <div className={styles.sectionHeading}>
                    <div>
                      <h2>オーナー権限</h2>
                      <p>組織の最上位権限を既存メンバーへ移譲します。</p>
                    </div>
                  </div>
                  {canTransfer ? (
                    transferCandidates.length > 0 ? (
                      <div className={styles.transferArea}>
                        <div className="field">
                          <label htmlFor="owner-transfer-target">移譲先メンバー</label>
                          <select
                            id="owner-transfer-target"
                            value={transferTargetUserId}
                            onChange={(event) => setTransferTargetUserId(event.target.value)}
                          >
                            <option value="">選択してください</option>
                            {transferCandidates.map((member) => (
                              <option key={member.user_id} value={member.user_id}>
                                {roleLabels[member.role]} · {member.user_id}
                              </option>
                            ))}
                          </select>
                        </div>
                        <div className={styles.transferExplanation}>
                          <strong>移譲後の権限</strong>
                          <p>選択したメンバーが新しいオーナーになり、あなたは管理者になります。seat数は変わりません。</p>
                        </div>
                        <button
                          className="button"
                          type="button"
                          onClick={handleTransferOwnership}
                          disabled={isSubmitting || !transferTargetUserId}
                        >
                          オーナー権限を移譲
                        </button>
                      </div>
                    ) : (
                      <p className={styles.permissionNote}>移譲できるメンバーがいません。先にメンバーを招待してください。</p>
                    )
                  ) : (
                    <p className={styles.permissionNote}>オーナー権限を移譲できるのは現在のオーナーだけです。</p>
                  )}
                </section>

                <section className={`panel ${styles.dangerSection}`}>
                  <div className={styles.sectionHeading}>
                    <div>
                      <h2>組織から退出</h2>
                      <p>退出すると、この組織に紐づくデータへアクセスできなくなります。</p>
                    </div>
                  </div>
                  {canLeave ? (
                    <div className={styles.dangerRow}>
                      <div>
                        <strong>自分のメンバー資格を削除</strong>
                        <p>再参加するには、組織管理者からもう一度招待を受ける必要があります。</p>
                      </div>
                      <button
                        className="button button-secondary"
                        type="button"
                        onClick={handleLeave}
                        disabled={isSubmitting}
                      >
                        この組織から退出
                      </button>
                    </div>
                  ) : currentRole === "owner" ? (
                    <p className={styles.permissionNote}>オーナーは退出できません。先に別のメンバーへオーナー権限を移譲してください。</p>
                  ) : (
                    <p className={styles.permissionNote}>退出操作を利用できません。</p>
                  )}
                </section>
              </>
            ) : (
              <div className="panel empty-state">組織設定を表示できませんでした。</div>
            )}
          </div>
        </div>
      )}
    </section>
  );
}
