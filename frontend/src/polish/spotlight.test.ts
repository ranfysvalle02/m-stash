// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest"
import { installSpotlight } from "./spotlight"

afterEach(() => {
  document.body.innerHTML = ""
  vi.restoreAllMocks()
})

function move(target: Element, clientX: number, clientY: number) {
  const event = new MouseEvent("pointermove", { bubbles: true, clientX, clientY })
  target.dispatchEvent(event)
}

describe("installSpotlight", () => {
  it("lights the card under the pointer relative to its own box", () => {
    vi.spyOn(window, "requestAnimationFrame").mockImplementation((cb) => {
      cb(0)
      return 1
    })
    document.body.innerHTML = `<a class="tutorial-card"><h2>Title</h2></a><p class="plain">x</p>`
    const card = document.querySelector<HTMLElement>(".tutorial-card")!
    vi.spyOn(card, "getBoundingClientRect").mockReturnValue({ left: 100, top: 40 } as DOMRect)
    const uninstall = installSpotlight()

    move(card.querySelector("h2")!, 130.4, 90.6)
    expect(card.style.getPropertyValue("--mx")).toBe("30px")
    expect(card.style.getPropertyValue("--my")).toBe("51px")

    const plain = document.querySelector<HTMLElement>(".plain")!
    move(plain, 5, 5)
    expect(plain.style.getPropertyValue("--mx")).toBe("")

    uninstall()
    move(card, 400, 400)
    expect(card.style.getPropertyValue("--mx")).toBe("30px")
  })
})
