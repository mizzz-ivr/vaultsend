import { expect, test, type Page, type Route } from "@playwright/test";

const now = "2026-08-07T00:00:00Z";
const ownerUser = {
  id: "settings-owner-1",
  email: "owner-settings@example.com",
  display_name: "Settings Owner",
  status: "active",
  created_at: now,
};
const memberUser = {
  id: "settings-member-1",
  email: "member-settings@example.com",
  display_name: "Settings Member",
  status: "active",
  created_at: now,
};

const ownerOrganization = {
  id: "org-settings-owner",
  name: "設定テスト組織",
  owner_user_id: ownerUser.id,
};
const memberOrganization = {
  id: "org-settings-member",
  name: "参加中プロジェクト",
  owner_user_id: "other-owner-1",
};

test("ownerが組織名を変更し、既存メンバーへオーナー権限を移譲できる", async ({ page }) => {
  const diagnostics = collectDiagnostics(page);
  const state = await installOrganizationSettingsRoutes(page, "owner");

  await page.goto("/organization-settings");

  await expect(page.getByRole("heading", { name: "組織設定" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "設定テスト組織", level: 2 })).toBeVisible();
  await expect(page.getByText("あなたの権限: オーナー", { exact: true })).toBeVisible();
  await expect(page.getByText("オーナーは退出できません。先に別のメンバーへオーナー権限を移譲してください。", { exact: true })).toBeVisible();

  await page.getByLabel("組織名").fill("設定テスト組織 Renamed");
  await page.getByRole("button", { name: "組織名を保存" }).click();
  await expect(page.getByText("組織名を変更しました。", { exact: true })).toBeVisible();
  expect(state.organizations[0].name).toBe("設定テスト組織 Renamed");

  await page.getByLabel("移譲先メンバー").selectOption("settings-admin-1");
  page.once("dialog", async (dialog) => {
    expect(dialog.type()).toBe("confirm");
    expect(dialog.message()).toContain("settings-admin-1");
    expect(dialog.message()).toContain("あなたの権限は管理者になります");
    await dialog.accept();
  });
  await page.getByRole("button", { name: "オーナー権限を移譲" }).click();

  await expect(
    page.getByText("オーナー権限を移譲しました。あなたは管理者として引き続き組織を管理できます。", {
      exact: true,
    }),
  ).toBeVisible();
  await expect(page.getByText("あなたの権限: 管理者", { exact: true })).toBeVisible();
  await expect(page.getByText("settings-admin-1", { exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "この組織から退出" })).toBeVisible();

  expect(state.organizations[0].owner_user_id).toBe("settings-admin-1");
  expect(state.membersByOrganization.get(ownerOrganization.id)).toContainEqual({
    user_id: ownerUser.id,
    role: "admin",
  });
  expect(state.membersByOrganization.get(ownerOrganization.id)).toContainEqual({
    user_id: "settings-admin-1",
    role: "owner",
  });
  expect(diagnostics.pageErrors).toEqual([]);
  expect(diagnostics.consoleErrors).toEqual([]);
});

test("memberが自分自身で組織から退出できる", async ({ page }) => {
  const diagnostics = collectDiagnostics(page);
  const state = await installOrganizationSettingsRoutes(page, "member");

  await page.goto("/organization-settings");

  await expect(page.getByRole("heading", { name: "参加中プロジェクト", level: 2 })).toBeVisible();
  await expect(page.getByText("あなたの権限: メンバー", { exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "組織名を保存" })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "オーナー権限を移譲" })).toHaveCount(0);

  page.once("dialog", async (dialog) => {
    expect(dialog.type()).toBe("confirm");
    expect(dialog.message()).toContain(memberOrganization.name);
    await dialog.accept();
  });
  await page.getByRole("button", { name: "この組織から退出" }).click();

  await expect(page.getByText("組織から退出しました。", { exact: true })).toBeVisible();
  await expect(page.getByText("設定できる組織はありません。", { exact: true })).toBeVisible();
  expect(state.organizations).toHaveLength(0);
  expect(state.membersByOrganization.get(memberOrganization.id)).not.toContainEqual({
    user_id: memberUser.id,
    role: "member",
  });
  expect(diagnostics.pageErrors).toEqual([]);
  expect(diagnostics.consoleErrors).toEqual([]);
});

test("adminは組織名を変更できるがオーナー移譲はできない", async ({ page }) => {
  const diagnostics = collectDiagnostics(page);
  const state = await installOrganizationSettingsRoutes(page, "admin");

  await page.goto("/organization-settings");

  await expect(page.getByText("あなたの権限: 管理者", { exact: true })).toBeVisible();
  await page.getByLabel("組織名").fill("管理者変更済み組織");
  await page.getByRole("button", { name: "組織名を保存" }).click();
  await expect(page.getByText("組織名を変更しました。", { exact: true })).toBeVisible();
  await expect(page.getByText("オーナー権限を移譲できるのは現在のオーナーだけです。", { exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "オーナー権限を移譲" })).toHaveCount(0);
  expect(state.organizations[0].name).toBe("管理者変更済み組織");
  expect(diagnostics.pageErrors).toEqual([]);
  expect(diagnostics.consoleErrors).toEqual([]);
});

type TestRole = "owner" | "admin" | "member";
type TestMember = { user_id: string; role: TestRole };

async function installOrganizationSettingsRoutes(
  page: Page,
  mode: "owner" | "admin" | "member",
) {
  const currentUser = mode === "member" ? memberUser : ownerUser;
  const organization = mode === "member"
    ? { ...memberOrganization }
    : mode === "admin"
      ? { ...ownerOrganization, owner_user_id: "other-owner-1" }
      : { ...ownerOrganization };
  const organizations = [organization];
  const members: TestMember[] = mode === "owner"
    ? [
        { user_id: ownerUser.id, role: "owner" },
        { user_id: "settings-admin-1", role: "admin" },
        { user_id: "settings-member-2", role: "member" },
      ]
    : mode === "admin"
      ? [
          { user_id: "other-owner-1", role: "owner" },
          { user_id: ownerUser.id, role: "admin" },
        ]
      : [
          { user_id: "other-owner-1", role: "owner" },
          { user_id: memberUser.id, role: "member" },
        ];
  const membersByOrganization = new Map<string, TestMember[]>([
    [organization.id, members],
  ]);

  await page.route("**/api/v1/**", async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    const path = url.pathname.replace(/^\/api\/v1/, "");
    const method = request.method();

    if (method === "GET" && path === "/auth/me") {
      await fulfillJSON(route, 200, { user: currentUser });
      return;
    }
    if (method === "GET" && path === "/orgs") {
      await fulfillJSON(route, 200, { items: organizations });
      return;
    }

    const organizationMatch = path.match(/^\/orgs\/([^/]+)$/);
    if (organizationMatch && method === "GET") {
      const organizationId = decodeURIComponent(organizationMatch[1]);
      const found = organizations.find((item) => item.id === organizationId);
      if (!found) {
        await fulfillJSON(route, 404, { code: "organization_not_found", message: "組織が見つかりません" });
        return;
      }
      await fulfillJSON(route, 200, {
        organization: found,
        members: membersByOrganization.get(organizationId) ?? [],
      });
      return;
    }
    if (organizationMatch && method === "PATCH") {
      const organizationId = decodeURIComponent(organizationMatch[1]);
      const found = organizations.find((item) => item.id === organizationId);
      if (!found) {
        await fulfillJSON(route, 404, { code: "organization_not_found", message: "組織が見つかりません" });
        return;
      }
      const body = request.postDataJSON() as { name: string };
      found.name = body.name;
      await fulfillJSON(route, 200, found);
      return;
    }

    const transferMatch = path.match(/^\/orgs\/([^/]+)\/owner-transfer$/);
    if (transferMatch && method === "POST") {
      const organizationId = decodeURIComponent(transferMatch[1]);
      const found = organizations.find((item) => item.id === organizationId);
      const body = request.postDataJSON() as { target_user_id: string };
      const currentMembers = membersByOrganization.get(organizationId) ?? [];
      if (!found || mode !== "owner") {
        await fulfillJSON(route, 403, { code: "forbidden", message: "オーナー権限が必要です" });
        return;
      }
      found.owner_user_id = body.target_user_id;
      const previousOwner = { user_id: currentUser.id, role: "admin" as const };
      const newOwner = { user_id: body.target_user_id, role: "owner" as const };
      membersByOrganization.set(
        organizationId,
        currentMembers.map((member) => {
          if (member.user_id === previousOwner.user_id) return previousOwner;
          if (member.user_id === newOwner.user_id) return newOwner;
          return member;
        }),
      );
      await fulfillJSON(route, 200, {
        organization: found,
        previous_owner: previousOwner,
        new_owner: newOwner,
      });
      return;
    }

    const leaveMatch = path.match(/^\/orgs\/([^/]+)\/leave$/);
    if (leaveMatch && method === "POST") {
      const organizationId = decodeURIComponent(leaveMatch[1]);
      if (mode === "owner") {
        await fulfillJSON(route, 409, {
          code: "owner_must_transfer",
          message: "オーナーは権限を移譲してから退出してください",
        });
        return;
      }
      const currentMembers = membersByOrganization.get(organizationId) ?? [];
      membersByOrganization.set(
        organizationId,
        currentMembers.filter((member) => member.user_id !== currentUser.id),
      );
      const index = organizations.findIndex((item) => item.id === organizationId);
      if (index >= 0) organizations.splice(index, 1);
      await fulfillJSON(route, 200, { status: "left" });
      return;
    }

    await fulfillJSON(route, 404, {
      code: "not_found",
      message: `未定義のE2E endpointです: ${method} ${path}`,
    });
  });

  return { organizations, membersByOrganization };
}

async function fulfillJSON(route: Route, status: number, body: unknown) {
  await route.fulfill({
    status,
    contentType: "application/json; charset=utf-8",
    body: JSON.stringify(body),
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
