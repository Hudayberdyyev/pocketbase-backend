# Architecture Overview

## Goals
- PocketBase is the only backend
- GetStream Chat is used only for messaging
- PocketBase is source of truth for users, projects, proposals, permissions
- Channels created server-side only
- Frontend never sees Stream API keys
- Soft delete everywhere

## Components
- PocketBase (Go hooks + REST)
  - Auth and user management
  - Projects and proposals
  - Access control
  - Stream Chat orchestration
- GetStream Chat (Go SDK)
  - Channel creation and membership
  - Token generation

## Data Ownership
- Users, projects, proposals, and conversations are stored in PocketBase
- Messages are stored only in GetStream
- Conversation records map PocketBase entities to Stream channel IDs

## Chat Lifecycle
1) Freelancer submits proposal
2) Client accepts proposal
3) Backend creates Stream channel and stores `stream_channel_id`
4) Frontend requests chat token from backend
5) Frontend connects directly to Stream using user token

## Security Principles
- Stream API keys never leave backend
- Channel creation only in backend
- Token scope limited to authenticated user
- Access to chat strictly tied to accepted proposals

