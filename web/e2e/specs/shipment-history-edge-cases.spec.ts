import { expect, test, type Page, type Route } from "@playwright/test";

const SHIPMENTS_PATH = "/api/v1/shipments";

const authUser = {
  user: {
    id: "e2e-user-1",
    email: "e2e@example.com",
    display_name: "E2E User",
    status: "active",
    created_at: "2026-07-01T00:00:00Z",
  },
};

test("送信履歴の2ページ目へ移動し、前のページへ戻れる", async ({ page }) => {
  const diagnostics = collectDiagnostics(page);
  const fixtures = createPaginationFixtures(12);

  await page.route("**/api/v1/auth/me", async (route) => {
    await fulfillJSON(route, 200, authUser);
  });
  await installShipmentCollectionRoutes(page, fixtures);

  await page.goto("/shipments");

  await expect(page.getByText("1〜10件 / 全12件", { exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: /ページング送信 01/ })).toBeVisible();
  await expect(page.getByRole("button", { name: /ページング送信 10/ })).toBeVisible();
  await expect(page.getByRole("button", { name: /ページング送信 11/ })).toHaveCount(0);
  await expect(
    page.getByLabel("送信詳細").getByRole("heading", { name: "ページング送信 01", level: 2 }),
  ).toBeVisible();

  const secondPageResponse = page.waitForResponse((response) => {
    const url = new URL(response.url());
    return (
      url.pathname === SHIPMENTS_PATH &&
      url.searchParams.get("offset") === "10" &&
      response.request().method() === "GET"
    );
  });
  await page.getByRole("button", { name: "次へ" }).click();
  expect((await secondPageResponse).status()).toBe(200);

  await expect(page.getByText("11〜12件 / 全12件", { exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: /ページング送信 11/ })).toBeVisible();
  await expect(page.getByRole("button", { name: /ページング送信 12/ })).toBeVisible();
  await expect(page.getByRole("button", { name: /ページング送信 01/ })).toHaveCount(0);
  await expect(
    page.getByLabel("送信詳細").getByRole("heading", { name: "ページング送信 11", level: 2 }),
  ).toBeVisible();
  await expect(page.getByRole("button", { name: "次へ" })).toBeDisabled();

  const firstPageResponse = page.waitForResponse((response) => {
    const url = new URL(response.url());
    return (
      url.pathname === SHIPMENTS_PATH &&
      url.searchParams.get("offset") === "0" &&
      response.request().method() === "GET"
    );
  });
  await page.getByRole("button", { name: "前へ" }).click();
  expect((await firstPageResponse).status()).toBe(200);

  await expect(page.getByText("1〜10件 / 全12件", { exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: /ページング送信 01/ })).toBeVisible();
  await expect(page.getByRole("button", { name: "前へ" })).toBeDisabled();

  expect(diagnostics.pageErrors).toEqual([]);
  expect(diagnostics.unexpectedConsoleErrors()).toEqual([]);
});

test("認証APIが401を返した場合は認証画面へ遷移する", async ({ page }) => {
  const diagnostics = collectDiagnostics(page, [401]);

  await page.route("**/api/v1/auth/me", async (route) => {
    await fulfillAPIError(route, 401, "authentication_required", "ログインが必要です");
  });
  await page.route("**/api/v1/shipments**", async (route) => {
    await fulfillJSON(route, 200, { items: [], limit: 10, offset: 0, total: 0 });
  });

  const unauthorizedResponse = page.waitForResponse(
    (response) =>
      new URL(response.url()).pathname === "/api/v1/auth/me" &&
      response.request().method() === "GET",
  );
  await page.goto("/shipments");
  expect((await unauthorizedResponse).status()).toBe(401);

  await expect(page).toHaveURL(/\/auth$/);
  await expect(page.getByRole("heading", { name: "おかえりなさい" })).toBeVisible();
  await expect(page.getByRole("button", { name: "ログイン" })).toBeVisible();

  expect(diagnostics.pageErrors).toEqual([]);
  expect(diagnostics.unexpectedConsoleErrors()).toEqual([]);
});

test("通知再送・論理削除に失敗した場合はエラーを表示して状態を維持する", async ({
  page,
}) => {
  const diagnostics = collectDiagnostics(page, [409, 503]);
  const fixture = createActionFixture();

  await page.route("**/api/v1/auth/me", async (route) => {
    await fulfillJSON(route, 200, authUser);
  });
  await page.route("**/api/v1/shipments**", async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    const pathname = url.pathname;
    const method = request.method();

    if (method === "GET" && pathname === SHIPMENTS_PATH) {
      await fulfillJSON(route, 200, {
        items: [fixture.listItem],
        limit: 10,
        offset: 0,
        total: 1,
      });
      return;
    }

    if (method === "GET" && pathname === `${SHIPMENTS_PATH}/${fixture.detail.id}`) {
      await fulfillJSON(route, 200, fixture.detail);
      return;
    }

    if (method === "POST" && pathname === `${SHIPMENTS_PATH}/${fixture.detail.id}/resend`) {
      await fulfillAPIError(
        route,
        503,
        "notification_service_unavailable",
        "通知サービスに一時的な問題が発生しています。",
      );
      return;
    }

    if (method === "DELETE" && pathname === `${SHIPMENTS_PATH}/${fixture.detail.id}`) {
      await fulfillAPIError(
        route,
        409,
        "shipment_delete_conflict",
        "処理中のため削除できません。時間をおいて再実行してください。",
      );
      return;
    }

    await fulfillAPIError(
      route,
      404,
      "e2e_route_not_found",
      `未定義のE2E routeです: ${method} ${pathname}`,
    );
  });

  await page.goto("/shipments");
  const detailPanel = page.getByLabel("送信詳細");
  await expect(
    detailPanel.getByRole("heading", { name: fixture.detail.subject, level: 2 }),
  ).toBeVisible();
  await expect(detailPanel.getByText("通知 1回", { exact: true })).toBeVisible();

  const resendResponse = page.waitForResponse(
    (response) =>
      new URL(response.url()).pathname === `${SHIPMENTS_PATH}/${fixture.detail.id}/resend` &&
      response.request().method() === "POST",
  );
  await detailPanel.getByRole("button", { name: "通知を再送" }).click();
  expect((await resendResponse).status()).toBe(503);

  await expect(
    page.getByText("通知サービスに一時的な問題が発生しています。", { exact: true }),
  ).toBeVisible();
  await expect(detailPanel.getByText("通知 1回", { exact: true })).toBeVisible();
  await expect(detailPanel.getByRole("button", { name: "通知を再送" })).toBeEnabled();

  page.once("dialog", async (dialog) => {
    expect(dialog.type()).toBe("confirm");
    expect(dialog.message()).toContain(fixture.detail.subject);
    await dialog.accept();
  });

  const deleteResponse = page.waitForResponse(
    (response) =>
      new URL(response.url()).pathname === `${SHIPMENTS_PATH}/${fixture.detail.id}` &&
      response.request().method() === "DELETE",
  );
  await detailPanel.getByRole("button", { name: "送信を削除" }).click();
  expect((await deleteResponse).status()).toBe(409);

  await expect(
    page.getByText("処理中のため削除できません。時間をおいて再実行してください。", {
      exact: true,
    }),
  ).toBeVisible();
  await expect(
    detailPanel.getByRole("heading", { name: fixture.detail.subject, level: 2 }),
  ).toBeVisible();
  await expect(detailPanel.getByText("送信済み", { exact: true })).toBeVisible();
  await expect(detailPanel.getByRole("button", { name: "通知を再送" })).toBeEnabled();
  await expect(detailPanel.getByRole("button", { name: "送信を削除" })).toBeEnabled();

  expect(diagnostics.pageErrors).toEqual([]);
  expect(diagnostics.unexpectedConsoleErrors()).toEqual([]);
});

function collectDiagnostics(page: Page, expectedHTTPStatuses: number[] = []) {
  const consoleErrors: string[] = [];
  const pageErrors: string[] = [];

  page.on("console", (message) => {
    if (message.type() === "error") consoleErrors.push(message.text());
  });
  page.on("pageerror", (error) => pageErrors.push(error.message));

  return {
    consoleErrors,
    pageErrors,
    unexpectedConsoleErrors() {
      return consoleErrors.filter(
        (message) =>
          !expectedHTTPStatuses.some(
            (status) =>
              message.includes(`status of ${status}`) ||
              message.includes(`status code ${status}`) ||
              message.includes(`HTTP ${status}`),
          ),
      );
    },
  };
}

async function installShipmentCollectionRoutes(
  page: Page,
  fixtures: ReturnType<typeof createPaginationFixtures>,
) {
  await page.route("**/api/v1/shipments**", async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    const pathname = url.pathname;
    const method = request.method();

    if (method === "GET" && pathname === SHIPMENTS_PATH) {
      const limit = parseNonNegativeInteger(url.searchParams.get("limit"), 10);
      const offset = parseNonNegativeInteger(url.searchParams.get("offset"), 0);
      await fulfillJSON(route, 200, {
        items: fixtures.listItems.slice(offset, offset + limit),
        limit,
        offset,
        total: fixtures.listItems.length,
      });
      return;
    }

    const detailMatch = pathname.match(/^\/api\/v1\/shipments\/([^/]+)$/);
    if (method === "GET" && detailMatch) {
      const id = decodeURIComponent(detailMatch[1]);
      const detail = fixtures.details.get(id);
      if (!detail) {
        await fulfillAPIError(route, 404, "shipment_not_found", "shipmentが見つかりません");
        return;
      }
      await fulfillJSON(route, 200, detail);
      return;
    }

    await fulfillAPIError(
      route,
      404,
      "e2e_route_not_found",
      `未定義のE2E routeです: ${method} ${pathname}`,
    );
  });
}

function createPaginationFixtures(count: number) {
  const listItems = Array.from({ length: count }, (_, index) => {
    const sequence = String(index + 1).padStart(2, "0");
    return {
      id: `shipment-page-${sequence}`,
      subject: `ページング送信 ${sequence}`,
      share_mode: index % 2 === 0 ? "recipient_restricted" : "url_shared",
      status: index % 3 === 0 ? "accessed" : "sent",
      created_at: `2026-07-${String(23 - index).padStart(2, "0")}T03:00:00Z`,
      expires_at: "2026-08-10T03:00:00Z",
      download_count: index,
      max_download_count: 20,
      file_count: 1,
    };
  });

  const details = new Map(
    listItems.map((item) => [
      item.id,
      {
        id: item.id,
        status: item.status,
        share_mode: item.share_mode,
        subject: item.subject,
        message: `${item.subject}の詳細です。`,
        expires_at: item.expires_at,
        max_download_count: item.max_download_count,
        download_count: item.download_count,
        last_download_at: item.download_count > 0 ? "2026-07-23T05:00:00Z" : undefined,
        files: [
          {
            id: `${item.id}-file`,
            file_name: `${item.id}.pdf`,
            size: 1024,
          },
        ],
        recipients: [],
        notification_summary: {
          total_notifications: 0,
          queued_count: 0,
          sent_count: 0,
          failed_count: 0,
        },
        recipient_summaries: [],
      },
    ]),
  );

  return { listItems, details };
}

function createActionFixture() {
  return {
    listItem: {
      id: "shipment-action-error-1",
      subject: "障害時確認用送信",
      share_mode: "recipient_restricted",
      status: "sent",
      created_at: "2026-07-23T03:00:00Z",
      expires_at: "2026-08-01T03:00:00Z",
      download_count: 0,
      max_download_count: 5,
      file_count: 1,
    },
    detail: {
      id: "shipment-action-error-1",
      status: "sent",
      share_mode: "recipient_restricted",
      subject: "障害時確認用送信",
      message: "再送・削除失敗時の画面状態を確認します。",
      expires_at: "2026-08-01T03:00:00Z",
      max_download_count: 5,
      download_count: 0,
      files: [
        {
          id: "shipment-action-error-file-1",
          file_name: "failure-check.pdf",
          size: 2048,
        },
      ],
      recipients: [
        {
          id: "shipment-action-error-recipient-1",
          email: "failure@example.com",
          status: "notified",
        },
      ],
      notification_summary: {
        total_notifications: 1,
        queued_count: 0,
        sent_count: 1,
        failed_count: 0,
        last_notification_at: "2026-07-23T03:00:00Z",
      },
      recipient_summaries: [
        {
          recipient_id: "shipment-action-error-recipient-1",
          email: "failure@example.com",
          recipient_status: "notified",
          notification_count: 1,
          last_notification_status: "sent",
          last_notification_type: "initial",
          last_notified_at: "2026-07-23T03:00:00Z",
          download_count: 0,
          has_downloaded: false,
        },
      ],
    },
  };
}

function parseNonNegativeInteger(value: string | null, fallback: number) {
  if (value === null) return fallback;
  const parsed = Number.parseInt(value, 10);
  return Number.isInteger(parsed) && parsed >= 0 ? parsed : fallback;
}

async function fulfillJSON(route: Route, status: number, body: unknown) {
  await route.fulfill({
    status,
    contentType: "application/json; charset=utf-8",
    body: JSON.stringify(body),
  });
}

async function fulfillAPIError(
  route: Route,
  status: number,
  code: string,
  message: string,
) {
  await fulfillJSON(route, status, {
    error: code,
    code,
    message,
    request_id: "e2e-shipment-history-edge-request",
  });
}
