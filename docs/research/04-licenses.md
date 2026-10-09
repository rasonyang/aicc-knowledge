# License review for an Apache-2.0 Go project (research date 2026-10-08)

Go module evidence: LICENSE files read from the module cache after `go get`, using the versions shown. Each file's pkg.go.dev license tab is `https://pkg.go.dev/<module>@<version>?tab=licenses`. Raw GitHub URLs follow the pattern `https://raw.githubusercontent.com/<org>/<repo>/<tag>/LICENSE`.

## Summary: no blocker found, but 3 items need attention
1. **bge-m3 is MIT, not Apache-2.0.** It is compatible. It is not distributed by us, so it needs no NOTICE, but docs should state its license.
2. **Meilisearch server is dual-licensed.** The root LICENSE says `SPDX: MIT AND BUSL-1.1`. The Community Edition is MIT. Enterprise Edition (EE) parts are BUSL-1.1 and are non-production only. Per the README, EE covers sharding/replication (v1.37+) and S3-streaming snapshots. userProvided embedders, hybrid search and swap-indexes are CE features. Use the official `getmeili/meilisearch` image with no EE flags. This is a CAVEAT, not a NO.
3. **BSD-3-Clause for excelize, efp and nfp.** This is not Apache, but it is compatible. It requires reproducing the copyright and license text when redistributing source or binary (the container image included). Put these in a THIRD_PARTY_LICENSES file or the NOTICE file.

## Table

| Component | SPDX | Evidence | How used | Apache-2.0 compat | NOTICE needed |
|---|---|---|---|---|---|
| github.com/xuri/excelize/v2 v2.11.0 | BSD-3-Clause | https://github.com/xuri/excelize/blob/v2.11.0/LICENSE (module cache LICENSE: "BSD 3-Clause License, Copyright (c) 2016-2026 The excelize Authors"; also "(c) 2011-2017 Geoffrey J. Teale") | linked | YES-with-NOTICE | Reproduce BSD text and both copyrights in third-party licenses; no Apache-style NOTICE |
| github.com/xuri/efp v0.0.2 | BSD-3-Clause | https://github.com/xuri/efp/blob/v0.0.2/LICENSE | linked (transitive) | YES-with-NOTICE | reproduce BSD text |
| github.com/xuri/nfp (v0.0.1 or the 2025 pseudo-version) | BSD-3-Clause | https://github.com/xuri/nfp/blob/master/LICENSE | linked (transitive) | YES-with-NOTICE | reproduce BSD text |
| github.com/richardlehane/mscfb v1.0.9 | Apache-2.0 | https://github.com/richardlehane/mscfb/blob/v1.0.9/LICENSE.txt | linked (transitive) | YES | No NOTICE file shipped, so none to propagate |
| github.com/richardlehane/msoleps v1.0.6 | Apache-2.0 | https://github.com/richardlehane/msoleps/blob/v1.0.6/LICENSE.txt | linked (transitive) | YES | No NOTICE file shipped |
| github.com/meilisearch/meilisearch-go v0.36.3 | MIT | https://github.com/meilisearch/meilisearch-go/blob/v0.36.3/LICENSE (Copyright 2020-2025 Meili SAS) | linked | YES-with-NOTICE (keep MIT text) | reproduce MIT text |
| github.com/aws/aws-sdk-go-v2 v1.47.1, service/s3 v1.114.1, config v1.33.7, credentials v1.20.7 | Apache-2.0 | https://github.com/aws/aws-sdk-go-v2/blob/main/LICENSE.txt (each module has LICENSE.txt) | linked | YES | **YES.** Root module has NOTICE.txt: "AWS SDK for Go / Copyright 2015 Amazon.com, Inc. or its affiliates / Copyright 2014-2015 Stripe, Inc." https://github.com/aws/aws-sdk-go-v2/blob/main/NOTICE.txt. Apache section 4(d) requires propagating it in the NOTICE file |
| github.com/aws/smithy-go v1.28.4 | Apache-2.0 | https://github.com/aws/smithy-go/blob/main/LICENSE | linked (transitive) | YES | **YES.** NOTICE reads "Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved." https://github.com/aws/smithy-go/blob/main/NOTICE |
| HF text-embeddings-inference (TEI) | Apache-2.0 | https://github.com/huggingface/text-embeddings-inference/blob/main/LICENSE (Apache 2.0 text, "Copyright 2022 Hugging Face"). The WebFetch of that file showed no HFOIL or other terms; I did not check the license history | separate container, not linked | YES | no. Link or document the image |
| BAAI/bge-m3 weights | MIT | https://huggingface.co/BAAI/bge-m3 (model card metadata `license: mit`) | model weights served by TEI, not distributed by us | YES | no (we do not redistribute). Give attribution in docs |
| github.com/go-chi/chi/v5 v5.3.2 | MIT | https://github.com/go-chi/chi/blob/v5.3.2/LICENSE | linked | YES-with-NOTICE | reproduce MIT text |
| github.com/jackc/pgx/v5 v5.11.0 | MIT | https://github.com/jackc/pgx/blob/v5.11.0/LICENSE | linked | YES-with-NOTICE | reproduce MIT text |
| github.com/sqlc-dev/sqlc v1.31.1 | MIT | https://github.com/sqlc-dev/sqlc/blob/v1.31.1/LICENSE | codegen tool; generated code is ours | YES | none; sqlc is not in the binary |
| github.com/pressly/goose/v3 v3.28.0 | MIT | https://github.com/pressly/goose/blob/v3.28.0/LICENSE | linked if embedded for migrations, otherwise a tool | YES-with-NOTICE if linked | reproduce MIT text if linked |
| github.com/oapi-codegen/oapi-codegen/v2 v2.8.0 | Apache-2.0 | https://github.com/oapi-codegen/oapi-codegen/blob/v2.8.0/LICENSE | codegen tool | YES | none, no NOTICE file |
| github.com/oapi-codegen/runtime v1.7.0 | Apache-2.0 | https://github.com/oapi-codegen/runtime/blob/v1.7.0/LICENSE | linked | YES | no NOTICE file |
| github.com/oasdiff/oasdiff v1.33.0 | Apache-2.0 | https://github.com/oasdiff/oasdiff/blob/v1.33.0/LICENSE | CI tool only | YES | none |
| go.opentelemetry.io/otel v1.47.0 | Apache-2.0 | https://github.com/open-telemetry/opentelemetry-go/blob/main/LICENSE | linked | YES | no NOTICE file |
| github.com/prometheus/client_golang v1.24.1 | Apache-2.0 | https://github.com/prometheus/client_golang/blob/main/LICENSE | linked | YES | **YES.** NOTICE has "Copyright 2012-2015 The Prometheus Authors. This product includes software developed at SoundCloud Ltd." https://github.com/prometheus/client_golang/blob/main/NOTICE |
| gopkg.in/yaml.v3 v3.0.1 | MIT + Apache-2.0 (mixed) | https://github.com/go-yaml/yaml/blob/v3.0.1/LICENSE and NOTICE. Most code is MIT; the files ported from libyaml are MIT, and the NOTICE says Copyright 2011-2016 Canonical | linked | YES-with-NOTICE | keep the MIT text and the NOTICE entry. The repository is archived (see Notes) |
| github.com/goccy/go-yaml v1.19.2 | MIT | https://github.com/goccy/go-yaml/blob/v1.19.2/LICENSE | linked | YES-with-NOTICE | reproduce MIT text |
| Meilisearch server (container) | MIT AND BUSL-1.1 | https://github.com/meilisearch/meilisearch/blob/main/LICENSE and https://github.com/meilisearch/meilisearch/blob/main/LICENSE-EE | separate process | CAVEAT | no |
| SeaweedFS (dev compose only) | Apache-2.0 | https://github.com/seaweedfs/seaweedfs/blob/master/LICENSE (Copyright 2025 Chris Lu) | separate process, dev only | YES | no |
| PostgreSQL | PostgreSQL License (BSD/MIT-like, OSI) | https://www.postgresql.org/about/licence/ | separate process | YES | no |

Other transitive modules from the same resolution: golang.org/x/net and golang.org/x/crypto are BSD-3-Clause (the Go Authors); github.com/zeebo/xxh3 is BSD-2-Clause; github.com/tiendc/go-deepcopy is MIT. Run `go-licenses report ./...` on the real module before release to cover all 297 modules.

## Notes

**Meilisearch Edition split.**
- The root LICENSE says "Part of this work fall under the Meilisearch Enterprise Edition (EE) and are licensed under the Business Source License 1.1. The other parts of this work are licensed under the MIT license." Its SPDX line is `MIT AND BUSL-1.1`.
- LICENSE-EE covers files in `enterprise_editions` modules or folders, plus files explicitly marked EE. Its Additional Use Grant allows only non-production use (testing, development, evaluation). Production use needs a commercial agreement. The work converts to MIT 4 years after publication.
- The README, as summarized by search, says CE is MIT and includes full-text, semantic and hybrid search. It says EE adds sharding and S3-streaming snapshots. Replication and sharding need EE v1.37 or later, per the Meilisearch docs.
- I did not find a Meilisearch statement that userProvided embedders or swap-indexes are EE, and nothing suggests they are. Neither feature is listed in the EE docs I saw.
- Action: pin the image and re-check the feature list before each upgrade. Do not document sharding or S3 snapshots as supported. Say in the docs that the project is not responsible for the Meilisearch edition the user runs.

**Apache-2.0 distribution.** Our NOTICE file should carry the AWS SDK NOTICE text, the smithy-go line, the Prometheus/SoundCloud line and the yaml.v3 Canonical line. A THIRD_PARTY_LICENSES file should reproduce the BSD-3 and MIT texts. The container image must include both, because the Go binary does not embed them.

**Copyleft.** None of the checked dependencies is GPL, AGPL or LGPL, and none carries a MPL or EPL file-level obligation.

**yaml choice.** gopkg.in/yaml.v3 is archived (it moved to go.yaml.in/yaml/v3 under the YAML org); the resolver also pulled go.yaml.in/yaml/v4 rc.6. go-yaml (goccy) is plain MIT. Either is compatible; prefer one for maintenance, not for license reasons.

**Not verified.** I did not read the pressly/goose, otel or oasdiff LICENSE files at pkg.go.dev. The module-cache copies were checked. Also, I did not open the TEI image's own bundled components (Rust crates, CUDA/MKL libraries in GPU images). They are separate software in a container we do not distribute, but if we publish a compose file that pulls the TEI image, our docs should link its license.
