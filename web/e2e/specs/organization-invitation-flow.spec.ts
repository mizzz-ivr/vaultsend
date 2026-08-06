import { expect, test, type Page, type Route } from "@playwright/test";

const now = "2026-08-06T00:00:00Z";
const owner = {
  id: "e2e-owner-1",
  email: "owner@example.com",
  display_name: "E2E Owner",
  status: "active",
  created_at: now,
};
const invitee = {
  id: "e2e-invitee-1",
  email: "invitee@example.com",
  display_name: "E2E Invitee",
  status: "active",
  created_at: now,
};
const organization = {
  id: "org-invitation-e2e",
  name: "VaultSend招待テスト",
  owner_user_id: owner.id,
};

test("ownerがメール招待を作成し、再送・取消できる", async ({ page }) => {
  const diagnostics = collectDiagnostics(page);
  const state = installInvitationManagementRoutes(page);

  await page.goto("/invitations");

  await expect(page.getByRole("heading", { name: "組織招待" })).toBeVisible();
  await expect(page.getByRole("heading", { name: organization.name })).toBeVisible();
  await expect(page.getByText("有効な招待 1件", { exact: true })).toBeVisible();

  await page.getByLabel("招待するメールアドレス").fill("new-member@example.com");
  await page.getByLabel("権限").selectOption("admin");
  await page.getByRole("button", { name: "招待メールを送る" }).click();

  await expect(
    page.getByText("new-member@example.com へ招待メールを送信キューに登録しました。", {
      exact: true,
    }),
  ).toBeVisible();
  const createdRow = page.getByRole("row").filter({ hasText: "new-member@example.com" });
  await expect(createdRow.getByText("管理者", { exact: true })).toBeVisible();
  await expect(createdRow.getByText("招待中", { exact: true })).toBeVisible();
  expect(state.invitations.some((invitation) => invitation.email === "new-member@example.com")).toBe(true);

  const pendingRow = page.getByRole("row").filter({ hasText: "pending@example.com" });
  await pendingRow.getByRole("button", { name: "再送" }).click();
  await expect(page.getByText("招待トークンを更新し、メールを再送しました。", { exact: true })).toBeVisible();
  expect(state.resendCount).toBe(1);

  page.once("dialog", async (dialog) => {
    expect(dialog.type()).toBe("confirm");
    expect(dialog.message()).toContain("pending@example.com");
    await dialog.accept();
  });
  await pendingRow.getByRole("button", { name: "取消" }).click();
  await expect(
    page.getByText("招待を取り消しました。既存の招待リンクは利用できません。", {
      exact: true,
    }),
  ).toBeVisible();
  await expect(pendingRow.getByText("取消済み", { exact: true })).toBeVisible();
  expect(state.revokeCount).toBe(1);

  expect(diagnostics.pageErrors).toEqual([]);
  expect(diagnostics.consoleErrors).toEqual([]);
});

test("招待先ユーザーが招待を確認して組織へ参加できる", async ({ page }) => {
  const diagnostics = collectDiagnostics(page);
  await installInvitationAcceptanceRoutes(page, true);

  await page.goto("/invite/e2e-invitation-token");

  await expect(
    page.getByRole("heading", { name: `${organization.name} への招待` }),
  ).toBeVisible();
  await expect(page.getByText("i***@example.com", { exact: true })).toBeVisible();
  await expect(page.getByText("invitee@example.com", { exact: true })).toBeVisible();

  await page.getByRole("button", { name: "この組織に参加する" }).click();

  await expect(
    page.getByRole("heading", { name: `${organization.name} に参加しました` }),
  ).toBeVisible();
  await expect(page.getByRole("link", { name: "組織管理を開く" })).toHaveAttribute(
    "href",
    "/organizations",
  );

  expect(diagnostics.pageErrors).toEqual([]);
  expect(diagnostics.consoleErrors).toEqual([]);
});

test("未ログイン時は招待URLを保持して認証画面へ移動できる", async ({ page }) => {
  const diagnostics = collectDiagnostics(page);
  await installInvitationAcceptanceRoutes(page, false);

  await page.goto("/invite/e2e-invitation-token");

  const authLink = page.getByRole("link", { name: "ログイン・新規登録へ" });
  await expect(authLink).toBeVisible();
  await authLink.click();
  await expect(page).toHaveURL(/\/auth\?next=%2Finvite%2Fe2e-invitation-token$/);

  expect(diagnostics.pageErrors).toEqual([]);
  expect(diagnostics.consoleErrors).toEqual([]);
});

type InvitationRecord = {
  id: string;
  organization_id: string;
  email: string;
  role: "admin" | "member";
  status: "pending" | "accepted" | "revoked" | "expired";
  expires_at: string;
  last_sent_at?: string;
  created_at: string;
};

function installInvitationManagementRoutes(page: Page) {
  const state = {
    invitations: [
      {
        id: "invitation-pending-1",
        organization_id: organization.id,
        email: "pending@example.com",
        role: "member" as const,
        status: "pending" as const,
        expires_at: "2026-08-13T00:00:00Z",
        last_sent_at: now,
        created_at: now,
      },
    ] as InvitationRecord[],
    resendCount: 0,
    revokeCount: 0,
  };

  void page.route("**/api/v1/**", async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    const method = request.method();

    if (method === "GET" && url.pathname === "/api/v1/auth/me") {
      await json(route, 200, { user: owner });
      return;
    }
    if (method === "GET" && url.pathname === "/api/v1/orgs") {
      await json(route, 200, { items: [organization] });
      return;
    }
    if (method === "GET" && url.pathname === `/api/v1/orgs/${organization.id}`) {
      await json(route, 200, {
        organization,
        members: [{ user_id: owner.id, role: "owner" }],
      });
      return;
    }
    if (method === "GET" && url.pathname === `/api/v1/orgs/${organization.id}/invitations`) {
      await json(route, 200, { items: state.invitations });
      return;
    }
    if (method === "POST" && url.pathname === `/api/v1/orgs/${organization.id}/invitations`) {
      const body = request.postDataJSON() as { email: string; role: "admin" | "member" };
      const invitation: InvitationRecord = {
        id: `invitation-created-${state.invitations.length + 1}`,
        organization_id: organization.id,
        email: body.email,
        role: body.role,
        status: "pending",
        expires_at: "2026-08-13T00:00:00Z",
        last_sent_at: now,
        created_at: now,
      };
      state.invitations.unshift(invitation);
      await json(route, 201, invitation);
      return;
    }

    const resendMatch = url.pathname.match(
      new RegExp(`^/api/v1/orgs/${organization.id}/invitations/([^/]+)/resend$`),
    );
    if (method === "POST" && resendMatch) {
      const invitation = state.invitations.find((item) => item.id === resendMatch[1]);
      if (!invitation) {
        await apiError(route, 404, "invitation_not_found", "招待が見つかりません");
        return;
      }
      state.resendCount += 1;
      invitation.expires_at = "2026-08-14T00:00:00Z";
      invitation.last_sent_at = "2026-08-07T00:00:00Z";
      await json(route, 200, invitation);
      return;
    }

    const revokeMatch = url.pathname.match(
      new RegExp(`^/api/v1/orgs/${organization.id}/invitations/([^/]+)$`),
    );
    if (method === "DELETE" && revokeMatch) {
      const invitation = state.invitations.find((item) => item.id === revokeMatch[1]);
      if (!invitation) {
        await apiError(route, 404, "invitation_not_found", "招待が見つかりません");
        return;
      }
      state.revokeCount += 1;
      invitation.status = "revoked";
      await json(route, 200, { status: "revoked" });
      return;
    }

    await apiError(route, 404, "not_found", `${method} ${url.pathname} は未定義です`);
  });

  return state;
}

async function installInvitationAcceptanceRoutes(page: Page, authenticated: boolean) {
  await page.route("**/api/v1/**", async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    const method = request.method();

    if (method === "GET" && url.pathname === "/api/v1/invitations/e2e-invitation-token") {
      await json(route, 200, {
        organization_id: organization.id,
        organization_name: organization.name,
        email_masked: "i***@example.com",
        role: "member",
        status: "pending",
        expires_at: "2026-08-13T00:00:00Z",
      });
      return;
    }
    if (method === "GET" && url.pathname === "/api/v1/auth/me") {
      if (!authenticated) {
        await apiError(route, 401, "unauthorized", "ログインが必要です");
        return;
      }
      await json(route, 200, { user: invitee });
      return;
    }
    if (method === "POST" && url.pathname === "/api/v1/invitations/e2e-invitation-token/accept") {
      if (!authenticated) {
        await apiError(route, 401, "unauthorized", "ログインが必要です");
        return;
      }
      await json(route, 200, {
        organization,
        member: { user_id: invitee.id, role: "member" },
        already_accepted: false,
      });
      return;
    }

    await apiError(route, 404, "not_found", `${method} ${url.pathname} は未定義です`);
  });
}

function collectDiagnostics(page: Page) {
  const consoleErrors: string[] = [];
  const pageErrors: string[] = [];
  page.on("console", (message) => {
    if (message.type() === "error") consoleErrors.push(message.text());
  });
  page.on("pageerror", (error) => pageErrors.push(error.message));
  return { consoleErrors, pageErrors };
}

async function json(route: Route, status: number, body: unknown) {
  await route.fulfill({
    status,
    contentType: "application/json; charset=utf-8",
    body: JSON.stringify(body),
  });
}

async function apiError(route: Route, status: number, code: string, message: string) {
  await json(route, status, {
    error: code,
    code,
    message,
    request_id: "e2e-invitation-request-id",
  });
}
