import { LoaderCircle, PenLine, Zap } from "lucide-react";
import type { VibeRecipeMeta } from "../api";
import { RecipeIcon } from "./recipeIcon";

const ACCENTS = new Set(["teal", "indigo", "rose", "amber", "emerald", "slate"]);

function accentClass(accent: string) {
  return ACCENTS.has(accent) ? `vibe-accent-${accent}` : "vibe-accent-teal";
}

// RecipeLaunchpad is the 6-card showcase on /vibe. Each card launches an instant
// live app (no model needed) or remixes the idea into the composer.
export default function RecipeLaunchpad({
  recipes,
  launchingId,
  disabled,
  onLaunch,
  onRemix,
}: {
  recipes: VibeRecipeMeta[];
  launchingId: string | null;
  disabled: boolean;
  onLaunch: (id: string) => void;
  onRemix: (recipe: VibeRecipeMeta) => void;
}) {
  if (recipes.length === 0) return null;
  return (
    <section className="vibe-launchpad">
      <div className="vibe-launchpad-head">
        <h2>Start from a recipe</h2>
        <p>
          Each one pairs public owner data with server-isolated per-user data — the whole m-stash
          ownership model, live in seconds.
        </p>
      </div>
      <div className="recipe-grid">
        {recipes.map((recipe) => {
          const launching = launchingId === recipe.id;
          return (
            <article className={`recipe-card ${accentClass(recipe.accent)}`} key={recipe.id}>
              <div className="recipe-card-head">
                <span className="recipe-badge">
                  <RecipeIcon name={recipe.icon} size={20} />
                </span>
                <div className="recipe-card-heading">
                  <h3>{recipe.name}</h3>
                  <p className="recipe-tagline">{recipe.tagline}</p>
                </div>
              </div>
              <ul className="recipe-splits">
                <li>
                  <span className="split-pill public">Public</span>
                  <span>{recipe.publicBlurb}</span>
                </li>
                <li>
                  <span className="split-pill private">Private</span>
                  <span>{recipe.privateBlurb}</span>
                </li>
              </ul>
              <div className="recipe-actions">
                <button
                  className="button primary"
                  disabled={disabled}
                  onClick={() => onLaunch(recipe.id)}
                  type="button"
                >
                  {launching ? (
                    <>
                      <LoaderCircle aria-hidden className="spin" size={16} /> Launching…
                    </>
                  ) : (
                    <>
                      <Zap aria-hidden size={16} /> Launch live app
                    </>
                  )}
                </button>
                <button
                  className="button secondary"
                  disabled={disabled}
                  onClick={() => onRemix(recipe)}
                  type="button"
                >
                  <PenLine aria-hidden size={15} /> Remix
                </button>
              </div>
            </article>
          );
        })}
      </div>
    </section>
  );
}
