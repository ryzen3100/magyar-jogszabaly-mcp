/**
 * Rate-limited HTTP client for Hungarian legislation from njt.hu.
 *
 * - 1200ms minimum delay between requests (government-friendly 1-2s window)
 * - User-Agent header identifying this MCP
 * - Retry on 429/5xx with exponential backoff
 */

const USER_AGENT =
  'Hungarian-Law-MCP/1.0 (+https://github.com/Ansvar-Systems/Hungarian-law-mcp; hello@ansvar.eu)';
const MIN_DELAY_MS = 1200;
const MAX_RETRIES = 3;

let lastRequestTime = 0;

async function rateLimit(): Promise<void> {
  const now = Date.now();
  const elapsed = now - lastRequestTime;
  if (elapsed < MIN_DELAY_MS) {
    await new Promise(resolve => setTimeout(resolve, MIN_DELAY_MS - elapsed));
  }
  lastRequestTime = Date.now();
}

export interface FetchResult {
  status: number;
  body: string;
}

async function requestWithRetry(url: string, init: RequestInit): Promise<FetchResult> {
  for (let attempt = 0; attempt <= MAX_RETRIES; attempt++) {
    let response: Response;
    try {
      response = await fetch(url, init);
    } catch (error) {
      if (attempt < MAX_RETRIES) {
        const backoff = Math.pow(2, attempt + 1) * 1000;
        const message = error instanceof Error ? error.message : String(error);
        console.log(`  Network error for ${url}: ${message}. Retrying in ${backoff}ms...`);
        await new Promise(resolve => setTimeout(resolve, backoff));
        continue;
      }
      throw error;
    }

    if ((response.status === 429 || response.status >= 500) && attempt < MAX_RETRIES) {
      const backoff = Math.pow(2, attempt + 1) * 1000;
      console.log(`  HTTP ${response.status} for ${url}, retrying in ${backoff}ms...`);
      await new Promise(resolve => setTimeout(resolve, backoff));
      continue;
    }

    return { status: response.status, body: await response.text() };
  }

  throw new Error(`Failed to fetch ${url} after ${MAX_RETRIES} retries`);
}

/**
 * Fetch a URL with rate limiting and retries on transient failures.
 */
export async function fetchWithRateLimit(url: string): Promise<FetchResult> {
  await rateLimit();

  return requestWithRetry(
    url,
    {
      headers: {
        'User-Agent': USER_AGENT,
        'Accept': 'text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8',
      },
      redirect: 'follow',
    }
  );
}

export async function postJsonWithRateLimit(url: string, payload: unknown): Promise<FetchResult> {
  await rateLimit();

  return requestWithRetry(
    url,
    {
      method: 'POST',
      headers: {
        'User-Agent': USER_AGENT,
        'Accept': 'text/html,application/json,*/*',
        'Content-Type': 'application/json; charset=utf-8',
      },
      body: JSON.stringify(payload),
      redirect: 'follow',
    }
  );
}
