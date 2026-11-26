// This wrapper is served together with mstash.js as a classic script.
(function (global) {
  const script = document.currentScript;
  const defaultApi = script?.src ? new URL(script.src).origin : "";

  class MStashClient {
    #client;

    constructor({ apiUrl = defaultApi, appKey, token = null } = {}) {
      if (typeof appKey !== "string" || !appKey.startsWith("pk_")) {
        throw new Error("MStashClient needs a publishable app key (pk_...)");
      }
      if (typeof token === "string" && token.startsWith("sk_")) {
        throw new Error("Secret keys must not be used in a browser");
      }
      this.#client = createClient({ api: apiUrl, app: appKey, token });
    }

    get token() { return this.#client.token; }
    signup(input) { return this.#client.signup(input); }
    login(input) { return this.#client.login(input); }
    logout() { this.#client.logout(); }
    savePersonal(type, record) { return this.#client.me.create(type, record); }
    updatePersonal(type, slug, record) { return this.#client.me.update(type, slug, record); }
    getPersonal(type, slug) { return this.#client.me.get(type, slug); }
    getShared(type, slug) { return this.#client.shared.get(type, slug); }
    getPublicShared(type, slug) { return this.#client.public.get(type, slug); }
  }

  const instance = new MStashClient({
    apiUrl: script?.dataset.apiUrl || defaultApi,
    appKey: script?.dataset.appKey,
  });
  global.MStashClient = MStashClient;
  global.mStash = instance;
})(window);
