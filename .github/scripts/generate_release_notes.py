import os
import subprocess
import time

import requests

MAX_CHARS_PER_CHUNK = 12000
MAX_DIFF_CHARS = 1200
INCLUDED_PATHS = [
    "cmd",
    "internal",
    "config",
    "models",
    "web",
    "migrations",
    "*.go",
    "go.mod",
    "go.sum",
]


def get_commit_data():
    try:
        tags = (
            subprocess.check_output(["git", "tag", "--sort=-creatordate"])
            .decode()
            .split()
        )
        if not tags:
            log_range = ["git", "log", "--pretty=format:%h"]
        elif len(tags) >= 2:
            # Check if there are commits between the latest tag and HEAD, otherwise use the range between latest two tags
            head_commits = subprocess.check_output(["git", "log", f"{tags[0]}..HEAD", "--pretty=format:%h"]).decode(errors="ignore").split()
            if head_commits:
                log_range = ["git", "log", f"{tags[0]}..HEAD", "--pretty=format:%h"]
            else:
                log_range = ["git", "log", f"{tags[1]}..{tags[0]}", "--pretty=format:%h"]
        else:
            head_commits = subprocess.check_output(["git", "log", f"{tags[0]}..HEAD", "--pretty=format:%h"]).decode(errors="ignore").split()
            if head_commits:
                log_range = ["git", "log", f"{tags[0]}..HEAD", "--pretty=format:%h"]
            else:
                log_range = ["git", "log", tags[0], "--pretty=format:%h"]

        hashes = subprocess.check_output(log_range).decode(errors="ignore").split()
        commit_data = []

        for h in hashes[:50]:
            msg = (
                subprocess.check_output(["git", "show", "-s", "--format=%s", h])
                .decode(errors="ignore")
                .strip()
            )

            show_cmd = [
                "git",
                "show",
                "--patch",
                "--stat",
                "--format=",
                h,
                "--",
            ] + INCLUDED_PATHS
            diff = subprocess.check_output(show_cmd).decode(errors="ignore")

            if not diff.strip():
                continue

            if len(diff) > MAX_DIFF_CHARS:
                diff = diff[:MAX_DIFF_CHARS] + "\n...[truncated]"
            commit_data.append(f"Commit: {h}\nMessage: {msg}\nChanges:\n{diff}")

        return commit_data
    except Exception as e:
        print(f"Error getting commit data: {e}")
        return []


def call_ai_with_retries(endpoint_url, api_token, payload, max_retries=3):
    headers = {
        "Authorization": f"Bearer {api_token}",
        "Content-Type": "application/json",
        "HTTP-Referer": "https://github.com/0xarchit/contestSync",
        "X-Title": "ContestSync Release Notes Generator",
    }
    for attempt in range(max_retries):
        try:
            print(f"Calling AI endpoint (attempt {attempt + 1})...")
            response = requests.post(
                endpoint_url, headers=headers, json=payload, timeout=120
            )
            if response.status_code == 429:
                wait_time = (attempt + 1) * 15
                print(f"Rate limited (429). Retrying in {wait_time}s...")
                time.sleep(wait_time)
                continue
            response.raise_for_status()
            data = response.json()
            return data["choices"][0]["message"]["content"]
        except Exception as e:
            print(f"AI API attempt {attempt + 1} failed: {e}")
            if attempt < max_retries - 1:
                time.sleep(5 * (attempt + 1))
            else:
                raise e
    return None


def main():
    output_file = "release_notes.md"
    
    api_base_url = (os.getenv("API_BASE_URL") or "").rstrip("/")
    api_token = os.getenv("API_TOKEN") or os.getenv("GH_MODELS_API_KEY")
    api_model = os.getenv("API_MODEL") or ""

    commit_data = get_commit_data()

    raw_lines = []
    for c in commit_data:
        lines = c.split("\n")
        if len(lines) > 1:
            raw_lines.append(lines[1])

    if raw_lines:
        raw_changelog = "## Commits\n" + "\n".join(raw_lines)
    else:
        raw_changelog = "Maintenance release."

    if not api_base_url or not api_token or not api_model:
        print("Missing API_BASE_URL, API_TOKEN, or API_MODEL. Writing raw commit log.")
        with open(output_file, "w", encoding="utf-8") as f:
            f.write(raw_changelog)
        return

    if not api_base_url.endswith("/chat/completions"):
        if api_base_url.endswith("/v1"):
            endpoint_url = f"{api_base_url}/chat/completions"
        else:
            endpoint_url = f"{api_base_url}/v1/chat/completions"
    else:
        endpoint_url = api_base_url

    if not commit_data:
        print("No commit data found, writing standard release description.")
        with open(output_file, "w", encoding="utf-8") as f:
            f.write("System optimizations and stability improvements.")
        return

    try:
        chunks = []
        current_chunk = ""
        for data in commit_data:
            if len(current_chunk) + len(data) > MAX_CHARS_PER_CHUNK:
                chunks.append(current_chunk)
                current_chunk = data + "\n\n"
            else:
                current_chunk += data + "\n\n"
        if current_chunk:
            chunks.append(current_chunk)

        summaries = []
        for chunk in chunks:
            payload = {
                "model": api_model,
                "messages": [
                    {
                        "role": "system",
                        "content": "You are an automated release notes generator. Provide immediate markdown bullet points summarizing these commits and diffs under Features, Fixes, and Refactors. Do NOT ask clarifying questions or acknowledge instructions.",
                    },
                    {"role": "user", "content": f"Commits and diffs to summarize:\n{chunk}"},
                ],
                "temperature": 0.2,
                "max_tokens": 1024,
            }
            summary = call_ai_with_retries(endpoint_url, api_token, payload)
            if summary:
                summaries.append(summary)
            time.sleep(1)

        combined_summaries = "\n\n".join(summaries).strip()
        if not combined_summaries:
            combined_summaries = raw_changelog

        final_payload = {
            "model": api_model,
            "messages": [
                {
                    "role": "system",
                    "content": "You are an automated GitHub release description generator. Output ONLY the release markdown with headers ## Key Features, ## Bug Fixes, and ## Technical Improvements. Do NOT output conversational filler or ask for input.",
                },
                {
                    "role": "user",
                    "content": f"Generate the release notes from these change summaries:\n\n{combined_summaries}",
                },
            ],
            "temperature": 0.2,
            "max_tokens": 2048,
        }
        final_notes = call_ai_with_retries(endpoint_url, api_token, final_payload)
        if final_notes and len(final_notes.strip()) > 20:
            with open(output_file, "w", encoding="utf-8") as f:
                f.write(final_notes.strip())
            print("Successfully generated release notes.")
        else:
            raise Exception("Empty or insufficient AI response received.")
    except Exception as e:
        print(f"Fallback to raw changelog due to error: {e}")
        with open(output_file, "w", encoding="utf-8") as f:
            f.write(raw_changelog)


if __name__ == "__main__":
    main()
