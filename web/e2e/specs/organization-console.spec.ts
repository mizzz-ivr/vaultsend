import { expect, test, type Page, type Route } from "@playwright/test";

const now = "2026-08-05T00:00:00Z";
const user = {
  id: "e2e-user-1",
  email: "e2e@example.com",
  display_name: "E2E User",
  status: "active",
  created_at: now,
};

test("組織を切り替え、権限に応じた課金・請求書・メンバー操作を確認できる", async ({ page }) => {
  const diagnostics = collectDiagnostics(page);
  const state = await installOrganizationRoutes(page);

  await page.goto("/organizations");

  await expect(page).toHaveURL(/\/organizations$/);
  await expect(page.getByRole("heading", { name: "組織管理" })).toBeVisible();
  await expect(page.getByText("E2E User", { exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: /VaultSend開発部/ })).toBeVisible();
  await expect(page.getByRole("heading", { name: "VaultSend開発部", level: 2 })).toBeVisible();
  await expect(page.getByText("あなたの権限: オーナー", { exact: true })).toBeVisible();

  const billingSection = page
    .getByRole("heading", { name: "プランと利用量" })
    .locator("xpath=ancestor::section");
  await expect(billingSection.getByText("PRO", { exact: true })).toBeVisible();
  await expect(billingSection.getByText("2 / 5", { exact: true })).toBeVisible();
  await expect(page.getByRole("heading", { name: "請求書" })).toBeVisible();
  await expect(page.getByText(/12,000/)).toBeVisible();
  await expect(page.getByRole("link", { name: "詳細" })).toHaveAttribute(
    "href",
    "https://invoice.stripe.test/invoice-1",
  );

  await page.getByLabel("追加するユーザーID").fill("new-user-1");
  await page.getByLabel("権限").selectOption("admin");
  await page.getByRole("button", { name: "メンバーを追加" }).click();
  await expect(page.getByText("メンバーを追加しました。", { exact: true })).toBeVisible();
  await expect(page.getByText("new-user-1", { exact: true })).toBeVisible();
  expect(state.membersByOrganization.get("org-owner")).toContainEqual({
    user_id: "new-user-1",
    role: "admin",
  });

  await page.getByRole("button", { name: /共同プロジェクト/ }).click();
  await expect(page.getByRole("heading", { name: "共同プロジェクト", level: 2 })).toBeVisible();
  await expect(page.getByText("あなたの権限: 管理者", { exact: true })).toBeVisible();
  await expect(page.getByRole("heading", { name: "プランと利用量" })).toHaveCount(0);
  await expect(page.getByRole("heading", { name: "請求書" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Proを申し込む" })).toHaveCount(0);

  expect(diagnostics.pageErrors).toEqual([]);
  expect(diagnostics.consoleErrors).toEqual([]);
});

test("所属組織がない状態から組織を作成できる", async ({ page }) => {
  const diagnostics = collectDiagnostics(page);
  await installOrganizationRoutes(page, { startEmpty: true });

  await page.goto("/organizations");

  await expect(
    page.getByText("所属組織はありません。最初の組織を作成してください。", { exact: true }),
  ).toBeVisible();
  await page.getByLabel("新しい組織名").fill("新規チーム");
  await page.getByRole("button", { name: "組織を作成" }).click();

  await expect(page.getByText("組織を作成しました。", { exact: true })).toBeVisible();
  await expect(page.getByRole("heading", { name: "新規チーム", level: 2 })).toBeVisible();
  await expect(page.getByText("あなたの権限: オーナー", { exact: true })).toBeVisible();

  expect(diagnostics.pageErrors).toEqual([]);
  expect(diagnostics.consoleErrors).toEqual([]);
});

async function installOrganizationRoutes(page: Page, options: { startEmpty?: boolean } = {}) {
  const organizations = options.startEmpty
    ? []
    : [
        { id: "org-owner", name: "VaultSend開発部", owner_user_id: user.id },
        { id: "org-admin", name: "共同プロジェクト", owner_user_id: "other-owner" },
      ];
  const membersByOrganization = new Map([
    [
      "org-owner",
      [
        { user_id: user.id, role: "owner" },
        { user_id: "member-1", role: "member" },
      ],
    ],
    [
      "org-admin",
      [
        { user_id: "other-owner", role: "owner" },
        { user_id: user.id, role: "admin" },
      ],
    ],
  ]);

  await page.route("**/api/v1/**", async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    const path = url.pathname.replace(/^\/api\/v1/, "");

    if (request.method() === "GET" && path === "/auth/me") {
      await fulfillJSON(route, 200, { user });
      return;
    }

    if (request.method() === "GET" && path === "/orgs") {
      await fulfillJSON(route, 200, { items: organizations });
      return;
    }

    if (request.method() === "POST" && path === "/orgs") {
      const body = request.postDataJSON() as { name: string };
      const created = {
        id: "org-created",
        name: body.name,
        owner_user_id: user.id,
      };
      organizations.push(created);
      membersByOrganization.set(created.id, [{ user_id: user.id, role: "owner" }]);
      await fulfillJSON(route, 201, created);
      return;
    }

    const organizationMatch = path.match(/^\/orgs\/([^/]+)$/);
    if (request.method() === "GET" && organizationMatch) {
      const organizationId = decodeURIComponent(organizationMatch[1]);
      const organization = organizations.find((item) => item.id === organizationId);
      if (!organization) {
        await fulfillJSON(route, 404, { code: "organization_not_found", message: "組織が見つかりません" });
        return;
      }
      await fulfillJSON(route, 200, {
        organization,
        members: membersByOrganization.get(organizationId) ?? [],
      });
      return;
    }

    const memberCollectionMatch = path.match(/^\/orgs\/([^/]+)\/members$/);
    if (request.method() === "POST" && memberCollectionMatch) {
      const organizationId = decodeURIComponent(memberCollectionMatch[1]);
      const body = request.postDataJSON() as { user_id: string; role: "owner" | "admin" | "member" };
      const member = { user_id: body.user_id, role: body.role };
      membersByOrganization.set(organizationId, [
        ...(membersByOrganization.get(organizationId) ?? []),
        member,
      ]);
      await fulfillJSON(route, 201, member);
      return;
    }

    const memberMatch = path.match(/^\/orgs\/([^/]+)\/members\/([^/]+)$/);
    if (request.method() === "DELETE" && memberMatch) {
      const organizationId = decodeURIComponent(memberMatch[1]);
      const memberId = decodeURIComponent(memberMatch[2]);
      membersByOrganization.set(
        organizationId,
        (membersByOrganization.get(organizationId) ?? []).filter(
          (member) => member.user_id !== memberId,
        ),
      );
      await fulfillJSON(route, 200, { status: "deleted" });
      return;
    }

    const billingMatch = path.match(/^\/orgs\/([^/]+)\/billing$/);
    if (request.method() === "GET" && billingMatch) {
      await fulfillJSON(route, 200, {
        plan: "pro",
        status: "active",
        usage: {
          current_month_shipments: 18,
          current_storage_bytes: 2_147_483_648,
        },
        members_count: 2,
        seat_limit: 5,
        current_seat_usage: 2,
        remaining_seats: 3,
        next_billing_at: "2026-09-01T00:00:00Z",
        remaining: {},
      });
      return;
    }

    const invoicesMatch = path.match(/^\/orgs\/([^/]+)\/invoices$/);
    if (request.method() === "GET" && invoicesMatch) {
      await fulfillJSON(route, 200, {
        invoices: [
          {
            invoice_id: `invoice-${decodeURIComponent(invoicesMatch[1])}`,
            amount: 12000,
            currency: "jpy",
            status: "paid",
            hosted_invoice_url: "https://invoice.stripe.test/invoice-1",
            invoice_pdf: "https://invoice.stripe.test/invoice-1.pdf",
            created_at: "2026-08-01T00:00:00Z",
            paid_at: "2026-08-01T01:00:00Z",
          },
        ],
        has_more: false,
      });
      return;
    }

    if (request.method() === "POST" && path === "/billing/checkout") {
      await fulfillJSON(route, 201, {
        session_id: "checkout-session-1",
        url: "https://checkout.stripe.test/session-1",
      });
      return;
    }

    await fulfillJSON(route, 404, {
      code: "not_found",
      message: `未定義のE2E endpointです: ${request.method()} ${path}`,
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
