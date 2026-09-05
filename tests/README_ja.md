# tests

*English version: [README.md](README.md)*

`make test`がExecDBをend-to-endで動かすために使うフィクスチャと
スクリプト(`.claude/rules/testing.md`参照)。`PLAN.md`の開発フェーズが
進むにつれて内容が拡充されていく。

ここに残るドライバ検証は`pgclient/`(Go、pgx)のみ。他言語(Python、
Node.js、Java、.NET、PHP、Ruby、Rust、ODBC)のPostgreSQLドライバ検証は
別リポジトリ[`execdb-drivers`](https://github.com/amisonnet8/execdb-drivers)
へ移動した。
