import React, { useState, useEffect, useRef } from 'react';
import { BrowserRouter, Link, NavLink, Routes, Route, useNavigate, useParams, useSearchParams } from "react-router";
import { EditIcon, TrashIcon, XIcon, PlusIcon, ThreeDotsIcon } from './icons';

function DeleteWishlistEntryButton({rowId, setWishlistUpToDate}) {
    async function doDelete() {
        try {
            const response = await fetch('/api/wishlist', {
                method: 'DELETE',
                headers: {
                    'Content-Type': 'application/json',
                },
                body: JSON.stringify({"ids": [rowId] })
            });

            if (!response.ok) {
                throw new Error(`HTTP error! status: ${response.status}`);
            }

            setWishlistUpToDate(false)
        } catch (error) {
            // XXX: handle this?
            console.log('Error deleting item: ' + error.message);
        }
    }

    return (<button
                onClick={doDelete}
                title="Delete wishlist entry">
                <TrashIcon/>
            </button>);
}

function EditWishlistEntryButton({row, isOwner, setWishlistUpToDate}) {
    const [formState, setFormState] = useState({});
    const [patchResponse, setPostResponse] = useState('');      

    const dialogRef = useRef(null);

    function updateField(field, value) {
        let copy = structuredClone(formState)
        copy[field] = value
        setFormState(copy)
    }
    
    async function doPatch() {
        try {
            const response = await fetch('/api/wishlist', {
                method: 'PATCH',
                headers: {
                    'Content-Type': 'application/json',
                },
                body: JSON.stringify({...formState,
                                      "id": row.id,
                                      "seq": row.seq})
            });

            if (!response.ok) {
                throw new Error(`HTTP error! status: ${response.status}`);
            }

            setWishlistUpToDate(false)
            setFormState({})
            dialogRef.current.close();
        } catch (error) {
            setPostResponse('Error sending data: ' + error.message);
        }
    }

    return (<>
                <button
                    onClick={() => dialogRef.current.showModal()}
                    title="Edit wishlist entry">
                    <EditIcon/>
                </button>

                <dialog ref={dialogRef}>
                    <button
                        onClick={() => dialogRef.current.close()}
                        title="Close window">
                        <XIcon/>
                    </button>

                    <h1> Edit {isOwner ? "Wishlist Entry" : "Buyer Notes"} </h1>
                    {isOwner ?
                     <>
                         <div className="input-pair-div">
                         Description <br/>
                         <input
                             type="text"
                             name="description"
                             className="login-signup-input"
                             value={formState.description !== undefined ? formState.description : row.description}
                             onChange={(event) => updateField("description", event.target.value)}
                         /> <br/>
                         </div>
                         <div className="input-pair-div">
                         Source <br/>
                         <input
                             type="text"
                             name="source"
                             className="login-signup-input"
                             value={formState.source !== undefined ? formState.source : row.source}
                             onChange={(event) => updateField("source", event.target.value)}
                         /> <br/>
                         </div>
                         <div className="input-pair-div">
                         Cost <br/>
                         <input
                             type="text"
                             name="cost"
                             className="login-signup-input"
                             value={formState.cost !== undefined ? formState.cost : row.cost}
                             onChange={(event) => updateField("cost", event.target.value)}
                         /> <br/>
                         </div>
                         <div className="input-pair-div">
                         Notes <br/>
                         <input
                             type="text"
                             name="owner_notes"
                             className="login-signup-input"
                             value={formState.owner_notes !== undefined ? formState.owner_notes : row.owner_notes ? row.owner_notes : ""}
                             onChange={(event) => updateField("owner_notes", event.target.value)}
                         />
                         </div>
                     </>
                     : /* !isOwner */
                     <>
                         <div className="input-pair-div">
                         Buyer Notes <br/>
                         <input
                             type="text"
                             name="buyer_notes"
                             className="login-signup-input"
                             value={formState.buyer_notes !== undefined ? formState.buyer_notes : row.buyer_notes ? row.buyer_notes : ""}
                             onChange={(event) => updateField("buyer_notes", event.target.value)}
                         />
                         </div>
                     </>
                    }
                    <br/>
                    <button onClick={doPatch}>
                        Update Item
                    </button>
                    {patchResponse && <p>{patchResponse}</p>}

                </dialog>
            </>
           );
}

function MaybeUrl({url}) {
    try {
        var urlObj = new URL(url);
        if (urlObj.protocol === "http:" || urlObj.protocol === "https:") {
            return <a href={url} target="_blank" rel="noopener noreferrer">{url}</a>
        }
    } catch (_) {
    }

    return <> {url} </>
}

function WishlistRow({row, isOwner, setWishlistUpToDate}) {
    const date = new Date(row.creation_time)
    
    return (<div className="wishlist-item-container">
                <div className="flex-header-footer">
                    <h3 className="wishlist-item-name"> {row.description} </h3>
                    <p className="wishlist-data"> {row.cost} </p>
                </div>
                <p className="wishlist-data"> Added {date.toDateString()} </p>
                <p className="wishlist-data"> <MaybeUrl url={row.source}/> </p>
                <p className="wishlist-notes"> {row.owner_notes} </p>
                {isOwner ? null : <p className="wishlist-notes"> {row.buyer_notes} </p>}
                <div className="flex-header-footer">
                    <EditWishlistEntryButton
                        row={row}
                        isOwner={isOwner}
                        setWishlistUpToDate={setWishlistUpToDate}/>
                    {!isOwner ? null :
                       <DeleteWishlistEntryButton
                           rowId={row.id}
                           setWishlistUpToDate={setWishlistUpToDate}/>
                    }
                </div>
            </div>);
}

function WishlistItems({wishlistData, setWishlistUpToDate, loggedInUserInfo}) {
    const isOwner = wishlistData.user.id === loggedInUserInfo.id;
    
    return (
        <div className="wishlist-list">
            {wishlistData.entries === null ? null : wishlistData.entries.map((row, rowIndex) => (
                <WishlistRow key={rowIndex}
                             row={row}
                             isOwner={isOwner}
                             setWishlistUpToDate={setWishlistUpToDate}/>
            ))}
        </div>
    );
}

function WishlistAdder({setWishlistUpToDate}) {
    const [formState, setFormState] = useState({
        description: '',
        source: '',
        cost: '',
        owner_notes: ''
    });
    const [postResponse, setPostResponse] = useState('');      

    const dialogRef = useRef(null);
    
    function handleDescription(event) {
        setFormState(formState => ({
            ...formState,
            description: event.target.value
        }))
    };

    function handleSource(event) {
        setFormState(formState => ({
            ...formState,
            source: event.target.value
        }))
    };

    function handleCost(event) {
        setFormState(formState => ({
            ...formState,
            cost: event.target.value
        }))
    };

    function handleOwnerNotes(event) {
        setFormState(formState => ({
            ...formState,
            owner_notes: event.target.value
        }))
    };

    async function doPost() {
        try {
            const response = await fetch('/api/wishlist', {
                method: 'POST',
                headers: {
                    'Content-Type': 'application/json',
                },
                body: JSON.stringify(formState)
            });

            if (!response.ok) {
                throw new Error(`HTTP error! status: ${response.status}`);
            }

            await response.json()
            setFormState({
                description: '',
                source: '',
                cost: '',
                owner_notes: ''
            })
            setWishlistUpToDate(false)
            dialogRef.current.close();
        } catch (error) {
            setPostResponse('Error sending data: ' + error.message);
        }
    }
    
    return (
        <div>
            <button onClick={() => dialogRef.current.showModal()}
                    title="Add item to wishlist">
                <PlusIcon/>
            </button>

            <dialog ref={dialogRef}>
                <button onClick={() => dialogRef.current.close()}
                        title="Close window">
                    <XIcon/>
                </button>

                <h1> Add to Wishlist </h1>
                Description <br/>
                <input
                    type="text"
                    name="description"
                    className="login-signup-input"
                    value={formState.description}
                    onChange={handleDescription}
                /> <br/>
                Source <br/>
                <input
                    type="text"
                    name="source"
                    className="login-signup-input"
                    value={formState.source}
                    onChange={handleSource}
                /> <br/>
                Cost <br/>
                <input
                    type="text"
                    name="cost"
                    className="login-signup-input"
                    value={formState.cost}
                    onChange={handleCost}
                /> <br/>            
                Notes <br/>
                <input
                    type="text"
                    name="notes"
                    className="login-signup-input"
                    value={formState.owner_notes}
                    onChange={handleOwnerNotes}              
                /> <br/>
                <button onClick={doPost}>
                    Add Item
                </button>
                {postResponse && <p>{postResponse}</p>}

            </dialog>            
        </div>
    );
}


function Login() {
    const [email, setEmail] = useState('');
    const [password, setPassword] = useState('');
    const [loginError, setLoginError] = useState('');
    const [doLogin, setDoLogin] = useState(null);
    let navigate = useNavigate();
    const [searchParams, ] = useSearchParams();

    function handleEmail(event) {
        setEmail(event.target.value);
    };

    function handlePassword(event) {
        setPassword(event.target.value);
    };

    useEffect(() => {
        if (!doLogin) {
            return
        }

        const login = async() => {
            try {
                const response = await fetch('/api/session', {
                    method: 'POST',
                    headers: {
                        'Content-Type': 'application/json',
                    },
                    body: JSON.stringify({
                        "email": email,
                        "password": password
                    })
                });
                
                const data = await response.json()
                
                if (!response.ok) {
                    setLoginError(data)
                } else {
                    localStorage.setItem("userInfo", JSON.stringify(data));
                    let redir = searchParams.get("redir")
                    if (redir) {
                        navigate(decodeURIComponent(redir))
                    } else {
                        navigate("/wishlist/" + data.id)
                    }
                }
            } catch (error) {
                setLoginError(error.message)
                console.log('Error sending data: ' + error.message);
            }
            setDoLogin(false);
        }
        login();
    }, [doLogin, email, navigate, searchParams, password]);

    return (
        <div className="login-signup">
            <h1> Login </h1>
            Email <br/>
            <input
                className="login-signup-input"
                type="email"
                name="email"
                onChange={handleEmail}
                disabled={doLogin}
            /> <br/>
            Password <br/>
            <input
                className="login-signup-input"
                type="password"
                name="user_password"
                onChange={handlePassword}
                disabled={doLogin}
            /> <br/>
            <button onClick={() => setDoLogin(true)}
                    disabled={doLogin}>
                Submit
            </button>
            {loginError && <p>{loginError}</p>}
            <p> don't have an account? </p>
            <nav>
                <Link to="/signup">
                    <button>
                        Signup
                    </button>
                </Link>
            </nav>
        </div>
    );
}

function Signup() {
    const [formState, setFormState] = useState({
        invite_code: '',
        first: '',
        last: '',
        email: '',
        password: ''
    });
    const formRef = useRef(null);
    let navigate = useNavigate();

    function handleInviteCode(event) {
        setFormState(formState => ({
            ...formState,
            invite_code: event.target.value
        }))
    };
    
    function handleFirstName(event) {
        setFormState(formState => ({
            ...formState,
            first: event.target.value
        }))
    };

    function handleLastName(event) {
        setFormState(formState => ({
            ...formState,
            last: event.target.value
        }))
    };

    function handleEmail(event) {
        setFormState(formState => ({
            ...formState,
            email: event.target.value
        }))
    };

    function handlePassword(event) {
        setFormState(formState => ({
            ...formState,
            password: event.target.value
        }))
    };

    async function handleSubmit(event) {
        event.preventDefault();
        if (!formRef.current.checkValidity()) {
            return
        }
        
        try {
            const response = await fetch('/api/signup', {
                method: 'POST',
                headers: {
                    'Content-Type': 'application/json',
                },
                body: JSON.stringify(formState)
            });

            const data = await response.json()
            
            if (!response.ok) {
                throw new Error(`HTTP error! status: ${response.status}`);
            }

            localStorage.setItem("userInfo", JSON.stringify(data));
            navigate("/wishlist/" + data.id)
        } catch (error) {
            console.log('Error sending data: ' + error.message);
        }
    }

    return (
        <div className="login-signup">
            <h1> Signup </h1>
            <form ref={formRef} onSubmit={handleSubmit}>
                Invite Code <br/>
                <input
                    className="login-signup-input"
                    type="text"
                    name="invite_code"
                    onChange={handleInviteCode}
                /> <br/>
                First Name <br/>
                <input
                    className="login-signup-input"
                    type="text"
                    name="firstname"
                    onChange={handleFirstName}
                /> <br/>
                Last Name <br/>
                <input
                    className="login-signup-input"
                    type="text"
                    name="lastname"
                    onChange={handleLastName}
                /> <br/>
                Email <br/>
                <input
                    className="login-signup-input"
                    type="email"
                    name="email"
                    required
                    onChange={handleEmail}
                /> <br/>            
                Password <br/>
                <input
                    className="login-signup-input"
                    type="password"
                    name="user_password"
                    onChange={handlePassword}              
                /> <br/>
                <input type="submit" value="Signup" />             
            </form>
            <p> Already have an account? </p>
            <nav>
                <Link to="/login">
                    <button>
                        Login
                    </button>
                </Link>
            </nav>
        </div>
    );
}

function LogoutButton() {
    async function doLogout() {
        try {
            localStorage.removeItem("userInfo")

            const response = await fetch('/api/session', {
                method: 'DELETE',
            });

            if (!response.ok) {
                throw new Error(`HTTP error! status: ${response.status}`);
            }
            await response
        } catch (error) {
            console.log('error logging out' + error.message);
        }
    }

    return (
        <Link to="/login">
            <button onClick={doLogout}>
                Logout
            </button>
        </Link>
    );
}

function WishlistSelector() {
    const [data, setData] = useState(null);
    const [loading, setLoading] = useState(true);
    const [error, setError] = useState(null);
    const dialogRef = useRef(null);
    
    useEffect(() => {
        const fetchData = async () => {
            try {
                const response = await fetch('/api/users', {
                    headers: {
                        'Content-Type': 'application/json',
                    },
                });
                if (!response.ok) {
                    throw new Error(`HTTP error! status: ${response.status}`);
                }
                setData(await response.json());
            } catch (error) {
                setError(error);
            } finally {
                setLoading(false);
            }
        };
        
        fetchData();
    }, []);

    if (loading) return <p>Loading data...</p>;
    if (error) return <p>Error: {error.message}</p>;

    return (
        <div>
            <button
                onClick={() => dialogRef.current.showModal()}
                title="Choose Wishlist Owner">
                <ThreeDotsIcon/>
            </button>
            <dialog ref={dialogRef}>
                <button
                    onClick={() => dialogRef.current.close()}
                    title="Close window">
                    <XIcon/>
                </button>
                
            <h1>Choose Wishlist Owner</h1>
            <table>
                <tbody>
                    {data["users"] === null ? null : data["users"].map((row, rowIndex) => (
                        <tr key={rowIndex}>
                            <td>
                                <NavLink to={"/wishlist/" + row["id"]}
                                         onClick={() => dialogRef.current.close()}>
                                    {row["first"] + " " + row["last"]}
                                </NavLink>
                            </td>
                        </tr>
                    ))}
                </tbody>
            </table>
            </dialog>
        </div>
    );    
}

function Wishlist() {
    let user = JSON.parse(localStorage.getItem("userInfo"))
    let navigate = useNavigate();
    let params = useParams();
    const [wishlistUpToDate, setWishlistUpToDate] = useState(true)
    const [wishlistData, setWishlistData] = useState(null);
    const [loading, setLoading] = useState(true);
    const [error, setError] = useState(null);
            
    useEffect(() => {
        let user = JSON.parse(localStorage.getItem("userInfo"))
        if (user === null) {
            // server side will validate this too, but don't even bother sending the request, just
            // re-direct on the client
            //
            // TODO: have the login send the user back to whatever wishlist they were looking for
            let redirect = encodeURIComponent("/wishlist/" + params.userId)
            navigate("/login?redir=" + redirect)
            return
        }
        
        const fetchData = async () => {
            try {
                let url = '/api/wishlist?' + new URLSearchParams({
                        userId: params.userId
                    }).toString()
                const response = await fetch(url, {
                    headers: {
                        'Content-Type': 'application/json',
                    },
                });

                if (!response.ok) {
                    throw new Error(`HTTP error! status: ${response.status}`);
                }
                setWishlistData(await response.json());
                setWishlistUpToDate(true)
            } catch (error) {
                setError(error);
            } finally {
                setLoading(false);
            }
        };
        
        fetchData();
    }, [params, wishlistUpToDate, setWishlistUpToDate, navigate]);

    
    if (loading) return <p>Loading data...</p>;
    if (error) return <p>Error: {error.message}</p>;
    
    return (
        <div className="app-body"> 
            <h1>{wishlistData.user.first}'s Wishlist</h1>
            <div className="wishlist-button-container">
                <WishlistSelector/>
                {parseInt(params.userId) === user.id ? <WishlistAdder setWishlistUpToDate={setWishlistUpToDate}/> : null}
            </div>
            <WishlistItems wishlistData={wishlistData}
                           setWishlistUpToDate={setWishlistUpToDate}
                           loggedInUserInfo={user}/>
            <LogoutButton/>
        </div>
    );
}

function Root() {
    let user = JSON.parse(localStorage.getItem("userInfo"))
    let navigate = useNavigate();

    useEffect(() => {
        if (user === null) {
            navigate("/login");
        } else {
            navigate("/wishlist/" + user.id)
        }
    }, [user, navigate]);
    return (<div/>)
}

export default function App() {
    return (
        <div className="top-container">
            <BrowserRouter>
                <Routes>
                    <Route path="/" element={<Root/>} />
                    <Route path="login" element={<Login/>}/>
                    <Route path="signup" element={<Signup/>}/>
                    <Route path="wishlist/:userId" element={<Wishlist/>}/>
                </Routes>
            </BrowserRouter>
        </div>)
}
