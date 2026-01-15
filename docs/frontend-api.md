# Frontend API Documentation

Base URL: `https://pocketbase-backend.up.railway.app`

All authenticated requests must include:
`Authorization: Bearer <JWT>`

## Auth

### Field options
- `role`: `client | freelancer`

### Sign up
POST `/api/collections/users/records`

Request
```json
{
  "email": "client@example.com",
  "password": "secret123",
  "passwordConfirm": "secret123",
  "role": "client",
  "name": "Client Name",
  "is_deleted": false
}
```

### Login
POST `/api/collections/users/auth-with-password`

Request
```json
{
  "identity": "client@example.com",
  "password": "secret123"
}
```

Response
```json
{
  "record": {
    "id": "USER_ID",
    "email": "client@example.com",
    "name": "Client Name",
    "role": "client",
    "is_deleted": false
  },
  "token": "JWT"
}
```

## Projects

### Field options
- `type`: `remote | onsite | hybrid`
- `status`: `open | in_progress | closed`

### List projects
GET `/api/collections/projects/records`

Notes:
- Client sees own projects
- Freelancer sees only `status = open`

### Create project (client only)
POST `/api/collections/projects/records`

Request
```json
{
  "title": "PocketBase",
  "description": "Build a PB backend",
  "type": "remote",
  "client_id": "CLIENT_USER_ID",
  "status": "open",
  "is_deleted": false
}
```

### Update project (client only)
PATCH `/api/collections/projects/records/{projectId}`

Request
```json
{
  "status": "in_progress"
}
```

## Proposals

### Field options
- `status`: `sent | accepted | rejected`

### List proposals
GET `/api/collections/proposals/records`

Notes:
- Client sees proposals for own projects
- Freelancer sees own proposals

### Create proposal (freelancer only)
POST `/api/collections/proposals/records`

Request
```json
{
  "project_id": "PROJECT_ID",
  "freelancer_id": "FREELANCER_USER_ID",
  "client_id": "CLIENT_USER_ID",
  "message": "I can do this project",
  "status": "sent",
  "is_deleted": false
}
```

### Accept / reject proposal (client only)
PATCH `/api/collections/proposals/records/{proposalId}`

Request
```json
{
  "status": "accepted"
}
```

## Chat

### Get chat token
POST `/chat/token`

Response
```json
{
  "user_id": "USER_ID",
  "token": "STREAM_CHAT_TOKEN"
}
```

### List conversations
GET `/chat/conversations`

Response
```json
[
  {
    "conversation_id": "CONVERSATION_ID",
    "stream_channel_id": "project_PROJECT_ID",
    "project": {
      "id": "PROJECT_ID",
      "title": "PocketBase",
      "status": "open"
    },
    "counterpart": {
      "id": "OTHER_USER_ID",
      "name": "Other User",
      "role": "client"
    },
    "proposal_id": "PROPOSAL_ID"
  }
]
```

