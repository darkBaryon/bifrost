#!/usr/bin/env python3
"""Rehearse upgrade/downgrade with an explicitly supplied baseline EE binary and synthetic SQLite data."""
import argparse
import importlib.util
import json
from pathlib import Path
import sqlite3
import tempfile

spec = importlib.util.spec_from_file_location('identity_smoke', Path(__file__).with_name('identity-smoke.py'))
smoke = importlib.util.module_from_spec(spec)
spec.loader.exec_module(smoke)


def save_config(node, config):
    (node.root/'config.json').write_text(json.dumps(config))


def scenario(binary, legacy, root, enabled, fallback):
    root.mkdir()
    dbpath = root/'config.db'
    cfg = {'providers':{},'client':{'enable_logging':False},'auth_config':{'is_enabled':enabled,'admin_username':'old admin!','admin_password':'short'},'config_store':{'enabled':True,'type':'sqlite','config':{'path':str(dbpath)}}}
    node = smoke.Node(legacy, root/'node', cfg, '')
    try:
        node.start()
        node.stop()
        with sqlite3.connect(dbpath) as src, sqlite3.connect(root/'before.db') as dst:
            src.backup(dst)
        if fallback:
            cfg['auth_config']['admin_password']='x'*73
            save_config(node,cfg)
        node.binary=str(Path(binary).resolve())
        node.start()
        state,_=node.expect(200,'/api/identity/status',{})
        assert state['initialized'] and state['is_auth_enabled']
        token=node.login('old admin!','short')
        node.expect(401,'/api/accounts/list',{},anonymous=True,headers={'Cookie':'token=legacy-session'})
        node.expect(200,'/api/identity/change-password',{'old_password':'short','new_password':'Migrated-password-1'},token=token)
        node.stop()
        cfg['auth_config']['admin_password']='Changed-file-password'
        save_config(node,cfg)
        node.start()
        node.expect(401,'/api/identity/login',{'username':'old admin!','password':'Changed-file-password'},anonymous=True)
        token=node.login('old admin!','Migrated-password-1')
        node.expect(401,'/api/identity/login',{'username':'old admin!','password':'short'},anonymous=True)
        events,_=node.expect(200,'/api/identity/password-events',{},token=token)
        assert events['items']
        (root/'preserved-events.json').write_text(json.dumps(events))
        node.stop()
        # 已关闭全部流量/节点，保留EE事件后恢复一致备份；不与新版本共库运行。
        with sqlite3.connect(root/'before.db') as src, sqlite3.connect(dbpath) as dst:
            src.backup(dst)
        with sqlite3.connect(dbpath) as db:
            db.execute('DELETE FROM sessions')
        cfg['auth_config'].update(is_enabled=True,admin_password='short')
        save_config(node,cfg)
        node.binary=str(Path(legacy).resolve())
        node.cookies.clear()
        node.start()
        node.expect(401,'/api/config',method='GET',anonymous=True)
        node.expect(200,'/api/session/login',{'username':'old admin!','password':'short'})
        node.expect(200,'/api/config',method='GET')
        node.expect(401,'/api/session/login',{'username':'old admin!','password':'Migrated-password-1'},anonymous=True)
    finally:
        node.stop()


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary',required=True)
    parser.add_argument('--legacy-binary',required=True)
    args=parser.parse_args()
    root=Path(tempfile.mkdtemp(prefix='bifrost-identity-upgrade-'))
    for enabled,fallback in ((True,False),(False,False),(True,True)):
        scenario(args.binary,args.legacy_binary,root/f'case-{enabled}-{fallback}',enabled,fallback)
    # 残缺迁移输入、缺DB均不能退回匿名；不带初始化密钥不能建首个账号。
    for name,extra,starts in (('incomplete',{'auth_config':{'is_enabled':True,'admin_username':'admin'}},False),('no-db',{'config_store':{'enabled':False}},False),('no-setup',{},True)):
        config={'providers':{},'client':{'enable_logging':False},**extra}
        node=smoke.Node(args.binary,root/name,config,'')
        try:
            try:
                node.start()
            except RuntimeError:
                assert not starts, 'empty instance failed to start'
            else:
                assert starts, 'invalid deployment started'
                node.expect(403,'/api/identity/initialize',{'setup_token':'','username':'admin','password':'Admin-password-1'})
                node.expect(401,'/api/accounts/list',{})
        finally:
            node.stop()
    print('PASS: enabled/disabled legacy migration, historic credentials, failed-file-hash DB fallback, restart authority, backup restore with baseline binary, invalid deployment fail-closed')
    print('Isolated evidence:',root)


if __name__=='__main__':
    main()
