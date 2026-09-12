// Package providers holds the systems of record readback reads claims back from:
// github (via gh), cloudflare, vercel, railway, netlify, fly, and plain http.
// Each provider authenticates the way its own CLI does; readback never stores
// credentials.
package providers
