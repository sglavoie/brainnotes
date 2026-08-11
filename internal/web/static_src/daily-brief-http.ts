export const jsonPostRequest = (csrfToken: string, body: string): RequestInit => ({
  method: "POST",
  headers: {
    Accept: "application/json",
    "Content-Type": "application/x-www-form-urlencoded; charset=UTF-8",
    "X-BN-CSRF": csrfToken,
  },
  body,
});

export const readResponsePayload = async (resp: Response): Promise<unknown> => {
  const contentType = resp.headers?.get("Content-Type") || "";
  try {
    if (contentType.includes("application/json")) {
      return await resp.json();
    }
    return await resp.text();
  } catch {
    return "";
  }
};
