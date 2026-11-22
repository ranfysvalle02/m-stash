import {
  Book,
  Box,
  Calendar,
  Dumbbell,
  Music,
  ShoppingCart,
  Sparkles,
  StickyNote,
  Trophy,
  User,
} from "lucide-react";
import type { ComponentType } from "react";

// recipeIcon maps the spec's closed icon vocabulary (shared with the Go backend)
// onto lucide glyphs for the launchpad and /discover strip.
const ICONS: Record<string, ComponentType<{ size?: number; "aria-hidden"?: boolean }>> = {
  music: Music,
  cart: ShoppingCart,
  book: Book,
  dumbbell: Dumbbell,
  note: StickyNote,
  calendar: Calendar,
  trophy: Trophy,
  user: User,
  box: Box,
  sparkles: Sparkles,
};

export function RecipeIcon({ name, size = 18 }: { name: string; size?: number }) {
  const Glyph = ICONS[name] ?? Box;
  return <Glyph aria-hidden size={size} />;
}
