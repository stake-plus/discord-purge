# How to Get Your Discord User Token

Discord does not provide an official way to obtain user tokens for automation.
The following method extracts the token your browser is already using when you
are logged in to Discord.

> **Security Warning:** Your Discord token grants full access to your account.
> Never share it with anyone. If you believe your token has been compromised,
> change your Discord password immediately — this invalidates all existing tokens.

---

## Method: Browser Developer Tools (Recommended)

### Step-by-step

1. **Open Discord in your web browser**
   - Go to [https://discord.com/app](https://discord.com/app)
   - Log in if you are not already logged in

2. **Open Developer Tools**
   - Press `F12` (or `Ctrl+Shift+I` on Windows/Linux, `Cmd+Option+I` on Mac)
   - The Developer Tools panel will open

3. **Go to the Network tab**
   - Click the **Network** tab at the top of the Developer Tools panel

4. **Filter for API requests**
   - In the filter/search box, type `api`
   - This filters the network traffic to only show Discord API calls

5. **Trigger an API request**
   - Click on any channel, send a message, or just navigate around Discord
   - You should see requests appearing in the Network tab

6. **Find the authorization header**
   - Click on any request that goes to `discord.com/api/...`
   - In the **Headers** section, scroll down to **Request Headers**
   - Find the header named `authorization`
   - The value next to it is your user token

7. **Copy the token**
   - Select and copy the entire token value
   - Do **not** include the word "authorization" — just the token value itself

---

## Method: Browser Console (Alternative)

1. Open Discord in your browser and log in
2. Press `F12` to open Developer Tools
3. Go to the **Console** tab
4. Paste this snippet and press Enter:

```javascript
(webpackChunkdiscord_app.push([[''],{},e=>{m=[];for(let c in e.c)m.push(e.c[c])}]),m).find(m=>m?.exports?.default?.getToken!==void 0).exports.default.getToken()
```

5. The console will output your token as a string — copy it

> **Note:** This method may not work if Discord updates their internal module structure.

---

## Using the Token

### Option A: Enter when prompted

Run `discord-purge` and paste the token when asked:

```
Enter your Discord user token: <paste your token here>
```

### Option B: Environment variable

Set the `DISCORD_TOKEN` environment variable before running:

**Windows (PowerShell):**
```powershell
$env:DISCORD_TOKEN = "your_token_here"
.\discord-purge.exe
```

**Windows (Command Prompt):**
```cmd
set DISCORD_TOKEN=your_token_here
discord-purge.exe
```

**Linux / macOS:**
```bash
export DISCORD_TOKEN="your_token_here"
./discord-purge
```

---

## Troubleshooting

| Problem | Solution |
|---------|----------|
| "Invalid token" error | Make sure you copied the full token with no extra spaces or quotes |
| Token stopped working | Tokens expire when you change your password or Discord invalidates them — get a fresh one |
| Can't find authorization header | Make sure you're looking at a request to `discord.com/api/...`, not a CDN or static file request |
| No API requests showing | Navigate around Discord (click channels, open DMs) to trigger API calls |
