# ATRPE — コード検証済み技術記事自動生成エンジン

ATRPE は [Temporal](https://temporal.io) ベースのワークフローシステムで、**コードが実際に動作確認された** 技術記事を [Zenn](https://zenn.dev) 向けに自動生成します。

## 特徴

- 🧪 **公開前にコードを検証** — すべてのコードブロックは実際の Go ツールチェーンで生成・コンパイル・テスト・lint されます
- 🔍 **マルチソース発見** — GitHub Trending、Hacker News、Zenn、Qiita、RSS フィードからトピックを収集
- ✍️ **人間の承認ゲート** — 公開前に必ず人間のレビューが入ります
- 🔄 **自動修正ループ** — 失敗した実験は最大3回まで自動で再試行・修正
- 📊 **フィードバック駆動** — 記事のエンゲージメント指標がトピック発見にフィードバックされます

## アーキテクチャ

```
GitHub Issue（トピック選択）
        │
        ▼
   Temporal ワークフロー
        │
   ┌────┼────────────────────────────┐
   ▼    ▼         ▼         ▼       ▼
Discover → Research → Design → Experiment → Verify
                                           │
                              ┌────────────┼────────────┐
                              ▼            ▼            ▼
                        GenerateArticle  PatchGen → DesignUpdate
                              │
                              ▼
                     人間の承認（GitHubコメント）
                              │
                              ▼
                           Publish
```

## クイックスタート

```bash
# 1. Temporal + PostgreSQL を起動
docker compose up -d

# 2. 設定
cp .env.example .env
# .env を編集して API キーなどを設定

# 3. パイプライン実行
go run ./cmd/pipeline
```

## ライセンス

MIT

---

🤖 *この記事も ATRPE によって生成・検証されています*
