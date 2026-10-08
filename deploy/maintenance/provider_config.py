"""Read the selected provider's URL from ordinary Codex TOML on Python 3.10+."""
import json,re

STRING=r'''(?:"(?:[^"\\]|\\.)*"|'[^']*')'''

def scalar(block,name):
    match=re.search(r'(?m)^\s*'+re.escape(name)+r'\s*=\s*('+STRING+r')\s*(?:#.*)?$',block)
    if not match:return ''
    value=match.group(1)
    return value[1:-1] if value.startswith("'") else json.loads(value)

def selected_provider_url(text):
    # Read only root configuration and one provider table. Do not guess the
    # first URL, which could belong to an unselected provider or a comment.
    tables=list(re.finditer(r'(?m)^\s*\[([^\]\n]+)\]\s*(?:#.*)?$',text))
    provider=scalar(text[:tables[0].start()] if tables else text,'model_provider')
    if not provider:return ''
    for index,table in enumerate(tables):
        name=table.group(1).strip()
        if not name.startswith('model_providers.'):continue
        name=name[len('model_providers.'):].strip()
        if name.startswith(('"',"'")):name=scalar('name = '+name,'name')
        if name!=provider:continue
        end=tables[index+1].start() if index+1<len(tables) else len(text)
        return scalar(text[table.end():end],'base_url')
    return ''
