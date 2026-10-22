#!/usr/bin/env python3
"""Run the repository SELECT against VALUES fixtures in an isolated PostgreSQL.

No production DSN, network, application migrations, or database writes. This is
SQL verification only, not a substitute for the Go repository integration tests.
Requires the pinned PostgreSQL image to already exist locally.
"""
import copy
import json
import re
import subprocess
from pathlib import Path

root = Path(__file__).resolve().parents[2]
sql=(root / 'backend/internal/repository/priority_scheduling.go').read_text().split('QueryContext(ctx, `',1)[1].split('`, pq.Array',1)[0].strip()
def lit(value):
    return "'"+json.dumps(value,separators=(',',':')).replace("'","''")+"'::jsonb"
base={'version':1,'status':200,'outcome':'success','terminal':'completed','truncated':False,'body_complete':True,'canceled':False,'client_disconnect':False,'downstream_error':False,'body_read_ms':300000,'downstream_write_ms':900000,'events':{'first_visible':410000},'attempts':[{'kind':'http','account_id':999,'start_ms':0,'status':503,'events':{'first_semantic':90000}},{'kind':'egress','account_id':1,'start_ms':0,'status':200,'events':{'first_semantic':100000}},{'kind':'http','account_id':1,'start_ms':1000,'status':200,'terminal':'completed','events':{'first_semantic':11000}}]}
usage=[];traces=[]
def add(detail,legacy=99000,minute=1):
    i=len(usage)+1
    usage.append(f"({i},1::bigint,NOW()-INTERVAL '{minute} minute',true,{legacy},1::numeric,1::numeric,1::numeric,1,0,0,0,''::text,'m'::text,NULL::bigint)")
    if detail is not None:traces.append(f'({i},{lit(detail)})')
    return i
for i,delay in enumerate([1000,2000,10000,20000,40000]):
    d=copy.deepcopy(base);d['attempts'][-1]['events']['first_semantic']=1000+delay;d['events']['first_visible']=400000+delay
    add(d,minute=10-i)
add(None,legacy=1)
valid_count=5
cases=[]
for key,value in [('terminal','incomplete'),('outcome','error'),('truncated',True),('canceled',True),('client_disconnect',True),('downstream_error',True),('body_complete',False),('version',2)]:
    d=copy.deepcopy(base);d[key]=value;cases.append(d)
for key,value in [('kind','egress'),('account_id',2),('status',503),('terminal','incomplete'),('error','timeout'),('start_ms',-1),('start_ms','bad')]:
    d=copy.deepcopy(base);d['attempts'][-1][key]=value;cases.append(d)
d=copy.deepcopy(base);d['attempts'][-1]['events']['first_semantic']=500;cases.append(d)
d=copy.deepcopy(base);d['attempts'][-1]['events']['first_semantic']='bad';cases.append(d)
d=copy.deepcopy(base);d['events']['first_write']=1;cases.append(d)
d=copy.deepcopy(base);d['events']['first_write']='bad';cases.append(d)
d=copy.deepcopy(base);d['attempts'].append({'kind':'http','account_id':2,'status':503});cases.append(d)
d=copy.deepcopy(base);d['attempts'].append({'kind':'egress','account_id':1,'status':200});cases.append(d)
d=copy.deepcopy(base);d['attempts']={};cases.append(d)
# The parent-only case must remove every physical child, including old failures.
cases[8]['attempts']=[cases[8]['attempts'][-1]]
for d in cases:add(d)
i=add(base);traces.append(f'({i},{lit(base)})')
fixtures='''WITH usage_logs(id,account_id,created_at,stream,first_token_ms,actual_cost,account_stats_cost,total_cost,input_tokens,output_tokens,cache_read_tokens,cache_creation_tokens,requested_model,model,group_id) AS (VALUES '''+','.join(usage)+'''),
request_timing_details(usage_log_id,detail) AS (VALUES '''+','.join(traces)+'''),
scheduled_test_plans AS (SELECT 0::bigint id,0::bigint account_id,true enabled,'{}'::jsonb pelican_config,''::text model_id,NOW() updated_at WHERE false),
scheduled_test_results AS (SELECT 0::bigint plan_id,NOW() created_at,''::text quality_round_id,'{}'::jsonb quality_judgment,''::text status,''::text error_message,'{}'::jsonb pelican_config,''::text quality_action WHERE false),
'''
columns='account_id,samples,p90_ttft_ms,quality_passed,quality_samples,profit_samples,revenue,base_cost,latest_quality_passed,latest_latency_at,p50_ttft_ms,p70_ttft_ms,p95_ttft_ms,selected_ttft_ms,latest_bad_latency_at,latency_unknown_samples,experience_samples,experience_p90_ms'
queries=[];expected=[]
for measurement in ['attributed_v1','legacy_forward']:
 for q,want in [(50,10000),(70,18000),(90,32000),(95,36000)]:
    args={1:'ARRAY[1]::bigint[]',2:"'m'",3:"NOW()-INTERVAL '1 hour'",4:"NOW()-INTERVAL '1 hour'",5:'NULL::bigint',6:str(q),7:"'"+measurement+"'",8:'15000',9:'NOW()'}
    body=re.sub(r'\$(\d+)',lambda m:args[int(m[1])],sql)
    queries.append('SELECT row_to_json(result) FROM ('+fixtures+body.removeprefix('WITH ')+') AS result('+columns+');')
    expected.append({'q':q,'measurement':measurement,'samples':5 if measurement=='attributed_v1' else len(usage),'selected':want if measurement=='attributed_v1' else 99000,'unknown':len(usage)-5 if measurement=='attributed_v1' else 0})

image = "postgres:18.1-alpine3.23"
subprocess.run(["docker", "image", "inspect", image], check=True, stdout=subprocess.DEVNULL)
result = subprocess.run([
    "docker", "run", "--rm", "-i", "--pull=never", "--network", "none", "--read-only",
    "--memory=256m", "--cpus=1", "--tmpfs", "/tmp:rw,mode=1777", "--user", "postgres",
    "--entrypoint", "sh", image, "-c",
    'initdb -D /tmp/pgdata -A trust >/dev/null && '
    'pg_ctl -D /tmp/pgdata -o \'-k /tmp -h "" -c shared_buffers=16MB\' '
    '-l /tmp/postgres.log start >/dev/null && '
    'psql -h /tmp -d postgres -XAt -v ON_ERROR_STOP=1',
], input="\n".join(queries), text=True, capture_output=True, check=True)
rows = [json.loads(line) for line in result.stdout.splitlines() if line.startswith("{")]
assert len(rows) == len(expected) == 8
for row, want in zip(rows, expected):
    assert row["samples"] == want["samples"], (row, want)
    assert row["latency_unknown_samples"] == want["unknown"], (row, want)
    assert abs(row["selected_ttft_ms"] - want["selected"]) < .01, (row, want)
    assert row["p90_ttft_ms"] == (32000 if want["measurement"] == "attributed_v1" else 99000)
    assert row["experience_samples"] == 25
    assert row["experience_p90_ms"] == 410000
    print(f"PASS {want['measurement']} P{want['q']}: selected={row['selected_ttft_ms']}ms "
          f"P90={row['p90_ttft_ms']}ms samples={row['samples']} unknown={row['latency_unknown_samples']}")
print("PASS: 8 SQL SELECT cases; 24 invalid/missing/ambiguous attribution fixtures remain unknown; experience P90=410000ms")
