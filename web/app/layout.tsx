import type { Metadata } from "next";
import Link from "next/link";
import type { ReactNode } from "react";
import "./globals.css";

export const metadata: Metadata = {
  title: "VaultSend",
  description: "期限・回数・受信者を制御できる大容量ファイル送信サービス",
};

export default function RootLayout({ children }: Readonly<{ children: ReactNode }>) {
  return (
    <html lang="ja">
      <body>
        <header className="site-header">
          <div className="shell header-inner">
            <Link className="brand" href="/" aria-label="VaultSend トップへ">
              <span className="brand-mark" aria-hidden="true">V</span>
              <span>VaultSend</span>
            </Link>
            <nav className="header-nav" aria-label="メインナビゲーション">
              <Link href="/shipments">送信履歴</Link>
              <Link href="/organizations">組織</Link>
              <Link href="/send">新規送信</Link>
              <Link className="button button-small" href="/auth">ログイン</Link>
            </nav>
          </div>
        </header>
        <main>{children}</main>
        <footer className="site-footer">
          <div className="shell footer-inner">
            <p>VaultSend — 大容量ファイルを、安全に、必要な相手へ。</p>
            <p>API連携版フロントエンド MVP</p>
          </div>
        </footer>
      </body>
    </html>
  );
}
