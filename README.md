# PocketBase + Stream Chat Backend

A mini freelance marketplace backend built with PocketBase; GetStream Chat is used exclusively for messaging.

## Features
- Auth users with roles: client, freelancer
- Projects and proposals with access rules
- Server-side Stream Chat channel creation on proposal acceptance
- Chat token endpoint for frontend
- Conversations endpoint for listing allowed channels
- Soft delete everywhere
- Rate limit: 50 req/sec per user_id (fallback to IP)

## Requirements
- Go 1.22+
- Stream Chat credentials

## Environment
Set in `.env` or shell:
```
STREAM_API_KEY=your_key
STREAM_API_SECRET=your_secret
```

## Run
```
make clean
make serve
```

Optional (first time):
```
go mod tidy
```

## Migrations
Schema migration is in `migrations/1768432378_init.go`.

JSON schema export is in `migrations/pb_schema.json` (importable from PocketBase admin UI).

## API Docs (Frontend)
See `docs/frontend-api.md`.

## Chat Flow
1) Freelancer submits a proposal.
2) Client accepts the proposal.
3) Backend creates Stream channel `project_{projectId}` with both members.
4) Frontend requests `/chat/token`, then connects to Stream.
5) Frontend lists `/chat/conversations` for allowed channels.

