The default API client used by the Rancher machine driver now uses the same
bounded HTTP 429 handling as file-backed storage clients. Clients share a
one-request-per-second budget and provider cooldown within their process.

Only GET requests may retry. Mutations still return their first result without
replay; request contexts and the existing HTTP timeout bound all waiting.

Regression tests exercise the public constructor, throttled volume reads,
cooldown across clients and rejected mutations. Go 1.27.1, CLO SDK 3.2.0;
Apache-2.0.
