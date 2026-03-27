/**
 * A typed HTTP client for making API requests.
 */

/** Configuration options for the API client. */
export interface ApiClientConfig {
  baseUrl: string;
  timeout?: number;
  headers?: Record<string, string>;
}

/** Represents an API response with typed body. */
export interface ApiResponse<T> {
  status: number;
  data: T;
  headers: Record<string, string>;
}

/** Error thrown when an API request fails. */
export class ApiError extends Error {
  constructor(
    public readonly status: number,
    public readonly body: unknown,
    message?: string
  ) {
    super(message || `API error: ${status}`);
    this.name = "ApiError";
  }
}

/**
 * Creates a new API client with the given configuration.
 */
export function createClient(config: ApiClientConfig) {
  const { baseUrl, timeout = 30000, headers = {} } = config;

  /**
   * Makes a GET request and returns the parsed response.
   */
  async function get<T>(path: string): Promise<ApiResponse<T>> {
    const response = await fetch(`${baseUrl}${path}`, {
      method: "GET",
      headers,
      signal: AbortSignal.timeout(timeout),
    });

    if (!response.ok) {
      throw new ApiError(response.status, await response.text());
    }

    const data = (await response.json()) as T;
    return {
      status: response.status,
      data,
      headers: Object.fromEntries(response.headers.entries()),
    };
  }

  /**
   * Makes a POST request with a JSON body.
   */
  async function post<T>(path: string, body: unknown): Promise<ApiResponse<T>> {
    const response = await fetch(`${baseUrl}${path}`, {
      method: "POST",
      headers: { ...headers, "Content-Type": "application/json" },
      body: JSON.stringify(body),
      signal: AbortSignal.timeout(timeout),
    });

    if (!response.ok) {
      throw new ApiError(response.status, await response.text());
    }

    const data = (await response.json()) as T;
    return {
      status: response.status,
      data,
      headers: Object.fromEntries(response.headers.entries()),
    };
  }

  return { get, post };
}

/** Type guard to check if an error is an ApiError. */
export function isApiError(error: unknown): error is ApiError {
  return error instanceof ApiError;
}
