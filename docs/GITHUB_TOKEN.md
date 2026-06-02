# GitHub Token Configuration

## Why use a GitHub token?

Without a token, GitHub API requests are limited to **60 requests per hour** per IP address. With a token, this limit increases to **5000 requests per hour**.

## How to get a GitHub token

1. Go to GitHub Settings: https://github.com/settings/tokens
2. Click "Generate new token" → "Generate new token (classic)"
3. Give it a descriptive name (e.g., "mcp-web-scrape")
4. Select scopes: **public_repo** (for public repositories) or **repo** (for private)
5. Click "Generate token"
6. **Copy the token immediately** - it won't be shown again!

## Configuration

Add the token to your `config.yaml`:

```yaml
github:
  token: "ghp_your_token_here"
```

Or set via environment variable:

```bash
export GITHUB_TOKEN="ghp_your_token_here"
```

## Security considerations

- **Never commit tokens** to version control
- Add `config.yaml` to `.gitignore`
- Use environment variables for production deployments
- Rotate tokens periodically
- Use different tokens for different environments

## Testing

Test if the token is working:

```bash
curl -H "Authorization: token YOUR_TOKEN" https://api.github.com/rate_limit
```

Response should show:
```json
{
  "resources": {
    "search": {
      "limit": 30,
      "remaining": 30,
      "reset": 1699999999
    }
  },
  "rate": {
    "limit": 5000,
    "remaining": 5000,
    "reset": 1699999999
  }
}
```

The `"rate": {"limit": 5000}` confirms the token is working.

## Troubleshooting

### 403 Forbidden after some requests
- Your token might have expired
- Check rate limit: `curl https://api.github.com/rate_limit`
- Without token: limit will be 60
- With token: limit should be 5000

### 401 Unauthorized
- Token is invalid or expired
- Regenerate the token and update config

### Token not working
- Ensure token has correct scopes (public_repo or repo)
- Check for typos in config file
- Verify environment variable is set correctly

## Benefits of using a token

✅ **83x more requests**: 5000/hour vs 60/hour
✅ **Better reliability**: Avoid rate limiting issues
✅ **Private repo access**: With `repo` scope
✅ **Higher API limits**: For search, GraphQL, etc.
✅ **Production ready**: Suitable for high-traffic deployments
