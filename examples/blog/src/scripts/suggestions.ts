// Loaded with import() by the search page, on the first focus of the
// search box.

// fillSuggestions fills the <datalist id={listID}> with the titles in the
// set:json block <script id={dataID}>.
export function fillSuggestions(dataID: string, listID: string): void {
  const data = document.getElementById(dataID);
  const list = document.getElementById(listID);
  if (!data || !list) {
    return;
  }
  const titles: string[] = JSON.parse(data.textContent ?? "[]");
  for (const title of titles) {
    const option = document.createElement("option");
    option.value = title;
    list.append(option);
  }
}
