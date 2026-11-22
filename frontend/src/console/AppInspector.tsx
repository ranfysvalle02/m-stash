// The Live Data Inspector lets an app owner browse the real users and records
// their app has stored — the same MongoDB-backed data the API serves — with a
// JSON preview and one-click, inline-confirmed deletes for clearing out test
// rows. Every call goes through owner-scoped /v1/builder endpoints, so the owner
// only ever sees their own app's tenant data; the server never exposes password
// hashes or another owner's app.

import { useCallback, useEffect, useState } from "react"
import { Database, LoaderCircle, RefreshCw, Users } from "lucide-react"
import { APIError, api } from "../api"
import type { InspectedResource, InspectedUser } from "../api"
import { useToast } from "./toastContext"
import { Highlighted } from "../runner/highlight"
import { ConfirmButton } from "./ConfirmButton"
import "./inspector.css"

type Tab = "records" | "users"

function recordJson(resource: InspectedResource): string {
  const obj: Record<string, unknown> = {
    id: resource.id,
    type: resource.type,
    slug: resource.slug,
    title: resource.title,
    visibility: resource.visibility,
  }
  if (resource.namespaceSlug) obj.namespace = `${resource.namespaceKind ?? "namespace"}:${resource.namespaceSlug}`
  if (resource.summary) obj.summary = resource.summary
  if (resource.data && Object.keys(resource.data).length > 0) obj.data = resource.data
  if (resource.content) obj.content = resource.content
  obj.createdAt = resource.createdAt
  obj.updatedAt = resource.updatedAt
  return JSON.stringify(obj, null, 2)
}

function shortDate(value: string): string {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return new Intl.DateTimeFormat("en", { day: "numeric", month: "short", year: "numeric" }).format(date)
}

export function AppInspector({ appId, appName, onChanged }: { appId: string; appName: string; onChanged?: () => void }) {
  const toast = useToast()
  const [tab, setTab] = useState<Tab>("records")

  const [records, setRecords] = useState<InspectedResource[]>()
  const [recordsError, setRecordsError] = useState("")
  const [recordsLoading, setRecordsLoading] = useState(false)
  const [typeFilter, setTypeFilter] = useState("")
  const [visFilter, setVisFilter] = useState("")
  const [expanded, setExpanded] = useState<string>()

  const [users, setUsers] = useState<InspectedUser[]>()
  const [usersError, setUsersError] = useState("")
  const [usersLoading, setUsersLoading] = useState(false)

  const loadRecords = useCallback(async () => {
    setRecordsLoading(true)
    setRecordsError("")
    try {
      const data = await api.listBuilderAppResources(appId, {
        type: typeFilter.trim() || undefined,
        visibility: visFilter || undefined,
      })
      setRecords(data)
    } catch (error) {
      setRecordsError(error instanceof APIError ? error.message : "Could not load records.")
    } finally {
      setRecordsLoading(false)
    }
  }, [appId, typeFilter, visFilter])

  const loadUsers = useCallback(async () => {
    setUsersLoading(true)
    setUsersError("")
    try {
      setUsers(await api.listBuilderAppUsers(appId))
    } catch (error) {
      setUsersError(error instanceof APIError ? error.message : "Could not load users.")
    } finally {
      setUsersLoading(false)
    }
  }, [appId])

  useEffect(() => {
    if (tab !== "records") return
    const handle = window.setTimeout(() => void loadRecords(), 220)
    return () => window.clearTimeout(handle)
  }, [tab, loadRecords])

  useEffect(() => {
    if (tab !== "users") return
    const handle = window.setTimeout(() => void loadUsers(), 0)
    return () => window.clearTimeout(handle)
  }, [tab, loadUsers])

  async function deleteRecord(id: string) {
    try {
      await api.deleteBuilderAppResource(appId, id)
      setRecords((all) => all?.filter((record) => record.id !== id))
      toast.success("Record deleted")
      onChanged?.()
    } catch (error) {
      toast.error(error instanceof APIError ? error.message : "Could not delete record.")
    }
  }

  async function deleteUser(id: string) {
    try {
      await api.deleteBuilderAppUser(appId, id)
      setUsers((all) => all?.filter((user) => user.id !== id))
      toast.success("User and their data deleted")
      onChanged?.()
    } catch (error) {
      toast.error(error instanceof APIError ? error.message : "Could not delete user.")
    }
  }

  return (
    <div className="inspector">
      <div className="inspector-head">
        <div className="inspector-tabs" role="tablist">
          <button type="button" role="tab" aria-selected={tab === "records"} className={tab === "records" ? "active" : ""} onClick={() => setTab("records")}>
            <Database aria-hidden="true" size={15} /> Records
          </button>
          <button type="button" role="tab" aria-selected={tab === "users"} className={tab === "users" ? "active" : ""} onClick={() => setTab("users")}>
            <Users aria-hidden="true" size={15} /> Users
          </button>
        </div>
        <button
          type="button"
          className="icon-button"
          aria-label="Refresh"
          title="Refresh"
          onClick={() => void (tab === "records" ? loadRecords() : loadUsers())}
        >
          <RefreshCw aria-hidden="true" size={15} />
        </button>
      </div>

      <p className="inspector-note">
        Live tenant data for <strong>{appName}</strong>. Deletes are permanent — great for clearing test rows.
      </p>

      {tab === "records" ? (
        <div className="inspector-body">
          <div className="inspector-filters">
            <input
              value={typeFilter}
              onChange={(event) => setTypeFilter(event.target.value)}
              placeholder="Filter by type (e.g. note)"
              aria-label="Filter by type"
              spellCheck={false}
            />
            <select value={visFilter} onChange={(event) => setVisFilter(event.target.value)} aria-label="Filter by visibility">
              <option value="">All visibility</option>
              <option value="private">Private</option>
              <option value="authenticated">Authenticated</option>
              <option value="public">Public</option>
            </select>
          </div>

          {recordsLoading && !records ? (
            <p className="inspector-state">
              <LoaderCircle aria-hidden="true" className="spin" size={16} /> Loading records…
            </p>
          ) : recordsError ? (
            <p className="inspector-state error">{recordsError}</p>
          ) : records && records.length > 0 ? (
            <ul className="inspector-list">
              {records.map((record) => {
                const open = expanded === record.id
                return (
                  <li key={record.id} className={open ? "open" : ""}>
                    <div className="inspector-row">
                      <button type="button" className="inspector-row-main" aria-expanded={open} onClick={() => setExpanded(open ? undefined : record.id)}>
                        <span className="inspector-row-title">{record.title || record.slug}</span>
                        <span className="inspector-row-meta">
                          <code>/{record.type}/{record.slug}</code>
                          <span className={`inspector-vis vis-${record.visibility}`}>{record.visibility}</span>
                          {record.namespaceSlug ? <span className="inspector-ns">{record.namespaceKind === "shared" ? "shared" : "@" + record.namespaceSlug}</span> : null}
                        </span>
                      </button>
                      <ConfirmButton variant="icon" confirmLabel="Delete record" ariaLabel={`Delete ${record.title || record.slug}`} onConfirm={() => void deleteRecord(record.id)} />
                    </div>
                    {open ? (
                      <pre className="inspector-json">
                        <Highlighted code={recordJson(record)} lang="json" />
                      </pre>
                    ) : null}
                  </li>
                )
              })}
            </ul>
          ) : (
            <p className="inspector-state">No records{typeFilter || visFilter ? " match these filters" : " yet"}.</p>
          )}
        </div>
      ) : (
        <div className="inspector-body">
          {usersLoading && !users ? (
            <p className="inspector-state">
              <LoaderCircle aria-hidden="true" className="spin" size={16} /> Loading users…
            </p>
          ) : usersError ? (
            <p className="inspector-state error">{usersError}</p>
          ) : users && users.length > 0 ? (
            <ul className="inspector-list">
              {users.map((user) => (
                <li key={user.id}>
                  <div className="inspector-row">
                    <div className="inspector-row-main static">
                      <span className="inspector-row-title">{user.email}</span>
                      <span className="inspector-row-meta">
                        {user.username ? <code>@{user.username}</code> : null}
                        <span className={`inspector-vis role-${user.role}`}>{user.role}</span>
                        <span className="inspector-ns">joined {shortDate(user.createdAt)}</span>
                      </span>
                    </div>
                    <ConfirmButton variant="icon" confirmLabel="Delete user and data" ariaLabel={`Delete ${user.email}`} onConfirm={() => void deleteUser(user.id)} />
                  </div>
                </li>
              ))}
            </ul>
          ) : (
            <p className="inspector-state">No users have signed up yet.</p>
          )}
        </div>
      )}
    </div>
  )
}
