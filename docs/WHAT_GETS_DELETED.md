# What Gets Deleted

This document describes in detail exactly what `discord-purge` removes from your
Discord account and how the deletion process works.

---

## ⚠️ DANGER — IRREVERSIBLE ACTION

**Everything deleted by this tool is permanently gone.** Discord does not provide
any way to recover deleted messages or removed reactions. There is no undo, no
recycle bin, no backup. Once a message is deleted via the API, it is erased from
Discord's servers.

**Before running this tool, understand:**
- Every message you have ever sent on Discord will be deleted
- Every reaction you have ever placed on anyone's message will be removed
- This includes messages and reactions from years ago
- This includes messages in servers you are still active in
- This cannot be reversed under any circumstances

---

## Phase 1: Server Messages

**What it deletes:** Every message you have sent in every server (guild) you are
currently a member of.

**Channel types covered:**
- Text channels
- Voice channel text chat
- Announcement / news channels
- Stage channels
- Forum posts (your posts and replies)
- All threads — public, private, active, and archived

**How it works:** Uses Discord's search API to find all messages authored by you
across the entire server in a single query. This is the most efficient method and
catches messages in every channel type, including threads and archived content.

**Limitations:**
- Only covers servers you are **currently a member of**. If you left a server,
  your messages there cannot be reached (you would need to rejoin first).
- Some servers may restrict search permissions — those will be skipped.

---

## Phase 2a: Open/Visible Direct Messages

**What it deletes:** Your messages in every DM and group DM that is currently
visible in your DM list.

**How it works:** Fetches all channels from your DM list, then uses the search
API on each channel to find and delete your messages.

---

## Phase 2b: Hidden/Closed Direct Messages

**What it deletes:** Your messages in DM channels that you have closed (hidden
from your DM list) but that can be re-opened through your relationships.

**How it works:**
1. Fetches your relationships (friends, blocked users, incoming/outgoing friend
   requests)
2. For each relationship, force-opens the DM channel (this makes it visible
   again in your DM list)
3. Searches and deletes your messages in each discovered channel

**What it catches that Phase 2a misses:**
- DMs with friends that you closed/hid
- DMs with people you blocked
- DMs associated with pending friend requests

---

## Phase 2c: Discord Data Package (Optional)

**What it deletes:** Your messages in any DM channel that ever existed on your
account, including conversations with deleted accounts and people you are no
longer connected to.

**How it works:** Parses the `messages/index.json` file from your Discord data
export to find every channel ID that has ever had messages. Then attempts to
search/delete your messages in each channel.

**What it catches that Phases 2a/2b miss:**
- DMs with users who deleted their accounts
- DMs with people you unfriended (who are no longer in your relationships)
- Group DMs you left
- Very old conversations with no current relationship

**How to get your data package:**
1. Open Discord and go to **Settings** > **Privacy & Safety**
2. Scroll down and click **Request All of My Data**
3. Wait for Discord to email you the download link (this can take up to 30 days)
4. Download and extract the ZIP file
5. Pass the extracted folder path to the tool:
   ```
   discord-purge --data-package /path/to/extracted-folder
   ```

---

## Phase 3: Reaction Removal

**What it removes:** Every reaction you have ever placed on any message (yours
or anyone else's) across all servers and all DM channels.

**How it works:** Since Discord has no "search reactions by user" API, this phase
must scan every single message in every channel:

1. Discovers all text-capable channels in every server (text, voice, news,
   stage, forum, media)
2. Discovers all threads — active threads, archived public threads, and archived
   private threads
3. Iterates through every message in every channel and thread
4. For each message, checks the `me: true` flag on each reaction
5. Removes your reaction via the API
6. Also scans all DM channels for reactions

**Why this is slow:** Unlike message deletion (which uses the efficient search
API), reaction removal must read every message in every channel because there is
no way to query "which messages did this user react to?" in Discord's API. For
large servers with many channels and messages, this phase can take a very long
time.

---

## Summary Table

| Content | Phase | Method |
|---------|-------|--------|
| Server text channel messages | Phase 1 | Search API |
| Thread messages (public/private/archived) | Phase 1 | Search API |
| Forum post messages | Phase 1 | Search API |
| Voice/stage text chat messages | Phase 1 | Search API |
| Announcement channel messages | Phase 1 | Search API |
| Open/visible DM messages | Phase 2a | Search API |
| Hidden DM messages (friends/blocked) | Phase 2b | Relationship discovery + Search API |
| Historical DM messages (deleted accounts) | Phase 2c | Data package + Search API |
| Group DM messages (open) | Phase 2a | Search API |
| Group DM messages (closed) | Phase 2c | Data package + Search API |
| Reactions on any message (servers) | Phase 3 | Full channel scan |
| Reactions on any message (DMs) | Phase 3 | Full channel scan |

---

## What Is NOT Deleted

- **Messages in servers you left** — You must be a member to delete messages.
  Rejoin the server first, then run the tool.
- **Messages from other users** — The tool only deletes your own messages.
- **Server settings, roles, or channels** — Only messages and reactions are
  affected.
- **Your account** — The tool does not delete or deactivate your Discord account.
- **Attachments on CDN** — While the message (and its attachment reference) is
  deleted, Discord may cache attachment files on their CDN for some time.
