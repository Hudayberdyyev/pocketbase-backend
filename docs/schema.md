# Database Schema

## Core Collections

### users (auth)
- email (auth)
- password (auth)
- role: `client | freelancer`
- name
- is_deleted (bool)
- created, updated

### projects
- title
- description
- type: `remote | onsite | hybrid`
- client_id → users
- status: `open | in_progress | closed`
- is_deleted
- created, updated

### proposals
- project_id → projects
- freelancer_id → users
- client_id → users
- message
- status: `sent | accepted | rejected`
- is_deleted
- created

Constraints:
- One proposal per freelancer per project

### conversations
Purpose: mapping between PocketBase and GetStream
- project_id → projects
- proposal_id → proposals
- stream_channel_id (string)
- is_deleted
- created

## Relationships
- users (client) 1 → many projects
- projects 1 → many proposals
- users (freelancer) 1 → many proposals
- projects 1 → 1 conversations (only after acceptance)
- proposals 1 → 1 conversations (only after acceptance)

## Soft Delete
- All collections include `is_deleted`
- No hard deletes

