// usableSandboxOwner returns the sandbox's generated owner login only while it
// can still work. Once the app is kept, the builder sets the real owner in the
// console and the generated one is replaced (its tokens are refused), so the
// page must stop showing it or signing the preview in with it.
export function usableSandboxOwner<T>(owner: T | null, claimed: boolean): T | null {
  return claimed ? null : owner
}
