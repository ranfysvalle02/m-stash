import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { ArrowUpRight, Sparkles, Wand2 } from "lucide-react";
import { api, type VibeRecipeMeta } from "../api";
import { RecipeIcon } from "./recipeIcon";

const ACCENTS = new Set(["teal", "indigo", "rose", "amber", "emerald", "slate"]);

function accentClass(accent: string) {
  return ACCENTS.has(accent) ? `vibe-accent-${accent}` : "vibe-accent-teal";
}

// RecipeStrip is the compact featured showcase on /discover. Each chip deep-links
// into the lab with ?recipe=<id> so the app auto-launches on arrival.
export default function RecipeStrip() {
  const [recipes, setRecipes] = useState<VibeRecipeMeta[]>([]);
  useEffect(() => {
    let active = true;
    void api
      .listVibeRecipes()
      .then((list) => {
        if (active) setRecipes(list);
      })
      .catch(() => {
        if (active) setRecipes([]);
      });
    return () => {
      active = false;
    };
  }, []);

  if (recipes.length === 0) return null;

  return (
    <section className="recipe-strip" aria-labelledby="recipe-strip-title">
      <div className="recipe-strip-head">
        <p className="eyebrow">
          <Sparkles aria-hidden size={15} /> Vibe recipes
        </p>
        <h2 id="recipe-strip-title">Launch a real backend in seconds</h2>
        <p>
          Pick a recipe and get a working app on a live sandbox — public owner data plus
          server-isolated per-user data, with auth already wired.
        </p>
      </div>
      <div className="recipe-strip-track">
        {recipes.map((recipe) => (
          <Link
            className={`recipe-chip ${accentClass(recipe.accent)}`}
            key={recipe.id}
            to={`/vibe?recipe=${encodeURIComponent(recipe.id)}`}
          >
            <span className="recipe-badge">
              <RecipeIcon name={recipe.icon} size={18} />
            </span>
            <strong className="recipe-chip-name">{recipe.name}</strong>
            <span className="recipe-chip-tagline">{recipe.tagline}</span>
            <span className="recipe-chip-go">
              Launch live <ArrowUpRight aria-hidden size={14} />
            </span>
          </Link>
        ))}
      </div>
      <Link className="button secondary" to="/vibe">
        <Wand2 aria-hidden size={16} /> Explore all recipes
      </Link>
    </section>
  );
}
