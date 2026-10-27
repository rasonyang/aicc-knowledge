# LLM output schema: string length bounds - live probe and docs (2026-10-27)

Verdict: CONFIRMED for llama-server (the grammar enforces `maxLength`); NOT CONFIRMED for OpenAI strict mode (the keyword is not in the documented list), so the client degrades instead of failing.

## Why
A small model (Qwen3-4B-Instruct on llama-server, `json_schema` output) repeated one sentence inside `answer` until the completion hit `max_tokens` in about 27% of first attempts on a 1300-section corpus (GitHub issue 3). The schema bounded the number of candidates and alternates, but no string. The request's `max_tokens` wins over the server's `-n`, so a server-side cap does not help either.

## llama-server (llama.cpp, the local `local-qwen` model)
- `response_format: {"type":"json_schema","json_schema":{"strict":true,"schema":{...}}}` is converted to a GBNF grammar. A string with `maxLength: N` becomes a bounded repetition, so the sampler cannot emit an (N+1)th character and must close the string.
- Probe: prompt asks for a sentence repeated at least 50 times as `answer`, schema `answer` `maxLength: 120`, `max_tokens: 600`. Result: `finish_reason: stop`, 33 completion tokens, the answer cut at the bound (mid-word). Without the bound the same kind of prompt runs to the token cap.
- Production schema (`internal/generate`, test `TestRealLLMStringBoundsEndALoop`, `KB_TEST_LLM_URL` set): the same loop prompt ends at the 300-character answer bound in about 1.3 s, `finish_reason: stop`.
- The grammar counts code points, as `utf8.RuneCountInString` does, so the Chinese limits line up.

## OpenAI strict structured outputs
- The "Supported schemas" section of https://developers.openai.com/api/docs/guides/structured-outputs lists `pattern` and `format` for strings and `minItems`/`maxItems` for arrays. It does not list `minLength` or `maxLength`; for fine-tuned models it names `minLength`, `maxLength`, `pattern` and `format` as unsupported.
- A community announcement (May 2025, "Structured Outputs gets nifty improvements") says string length limits were added. That is not the API reference, so it is treated as unconfirmed.
- Consequence: a hosted endpoint may answer HTTP 400 for the keyword. Other OpenAI-compatible servers (vLLM, DeepSeek) were not probed.

## What the code does
- Every string of the candidates schema and of the condense schema carries `maxLength`: the limit candidate validation applies plus 25% headroom (question and alternates 200 -> 250; answer EN 240 -> 300, ZH 90 -> 112 by default). Validation, which names the broken rule and feeds the retry, stays the authority: a string the grammar cut is longer than the limit and is rejected as `ANSWER_TOO_LONG` or `QUESTION_TOO_LONG`.
- Degrade, not fail (`internal/llm`): when a request whose schema has `minLength`/`maxLength` gets HTTP 400, the client sends it once more without those keywords. Only when that request is not refused with 400 were the bounds the cause: the client then remembers it for the rest of the run, logs one `LLM_STRING_BOUNDS_UNSUPPORTED` warning and the `generate` summary shows `string_bounds=off`. If the stripped request also gets 400 (llama-server does that for a prompt over the slot context, for example) the bounds stay on and the error is returned. Tests: `TestServerRejectingStringBoundsIsDegradedNotFailed`, `TestBadRequestOfBothFormsKeepsTheBoundsAndFails`.
- `max_tokens` is derived per request from the same bounds (the most a conforming answer can take; EN 0.5 and ZH 1.5 tokens per character, 48 per entry) and clamped to the client ceiling (`llm.DefaultMaxTokens`, 2048). It matters most for the condense request, whose cap is about 150 tokens instead of 2048. On a server that ignores the bounds it is still the backstop.
- A completion that hits the cap (`finish_reason: length`) is counted (`token_cap_hits` in the `generate` summary). Its retry no longer carries an empty assistant turn (there is no usable text to show); the problem is added to the original user message instead.
